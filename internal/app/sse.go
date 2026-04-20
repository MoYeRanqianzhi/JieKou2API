package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// JieKou SSE event types
type JieKouSSEEvent struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	Delta     string `json:"delta,omitempty"`
	ErrorText string `json:"errorText,omitempty"`
}

type jiekouError struct {
	Code    int    `json:"code"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// parseSSEError returns (errorMsg, isError). errorMsg is human-readable.
func parseSSEError(event *JieKouSSEEvent) (string, bool) {
	if event.Type != "error" {
		return "", false
	}
	if event.ErrorText == "" {
		return "unknown upstream error", true
	}
	var je jiekouError
	if err := json.Unmarshal([]byte(event.ErrorText), &je); err != nil {
		return event.ErrorText, true
	}
	if je.Message != "" {
		return je.Message, true
	}
	return event.ErrorText, true
}

func handleStreamResponse(w http.ResponseWriter, resp *http.Response, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":{"message":"Streaming not supported","type":"server_error"}}`, http.StatusInternalServerError)
		return
	}

	completionID := "chatcmpl-" + uuid.New().String()[:8]
	headersSent := false

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		if data == "[DONE]" {
			if headersSent {
				stop := "stop"
				chunk := NewStreamChunk(model, "", completionID, &stop)
				writeSSEChunk(w, chunk)
				fmt.Fprintf(w, "data: [DONE]\n\n")
				flusher.Flush()
			}
			break
		}

		var event JieKouSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			log.Printf("sse: parse error: %v (data=%s)", err, data)
			continue
		}

		if errMsg, isErr := parseSSEError(&event); isErr {
			log.Printf("sse: upstream error: %s", errMsg)
			if !headersSent {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				fmt.Fprintf(w, `{"error":{"message":"%s","type":"upstream_error"}}`, errMsg)
				return
			}
			chunk := NewStreamChunk(model, "\n\n[Error: "+errMsg+"]", completionID, nil)
			writeSSEChunk(w, chunk)
			flusher.Flush()
			continue
		}

		switch event.Type {
		case "text-start":
			if !headersSent {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				w.Header().Set("X-Accel-Buffering", "no")
				w.WriteHeader(http.StatusOK)
				headersSent = true

				roleChunk := NewStreamRoleChunk(model, completionID)
				writeSSEChunk(w, roleChunk)
				flusher.Flush()
			}
		case "text-delta":
			if event.Delta != "" {
				if !headersSent {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-cache")
					w.Header().Set("Connection", "keep-alive")
					w.Header().Set("X-Accel-Buffering", "no")
					w.WriteHeader(http.StatusOK)
					headersSent = true
				}
				chunk := NewStreamChunk(model, event.Delta, completionID, nil)
				writeSSEChunk(w, chunk)
				flusher.Flush()
			}
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("sse: stream read error: %v", err)
	}
}

func handleNonStreamResponse(w http.ResponseWriter, resp *http.Response, model string) {
	completionID := "chatcmpl-" + uuid.New().String()[:8]

	var fullContent strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event JieKouSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if errMsg, isErr := parseSSEError(&event); isErr {
			log.Printf("sse: upstream error in non-stream: %s", errMsg)
			errBody := fmt.Sprintf(`{"error":{"message":"%s","type":"upstream_error"}}`, errMsg)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, errBody)
			return
		}

		if event.Type == "text-delta" && event.Delta != "" {
			fullContent.WriteString(event.Delta)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("sse: non-stream read error: %v", err)
	}

	result := NewNonStreamResponse(model, fullContent.String(), completionID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

func writeSSEChunk(w io.Writer, chunk *OpenAIStreamChunk) {
	data, err := json.Marshal(chunk)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// --- Claude (Anthropic) SSE handlers ---

func writeClaudeSSE(w io.Writer, eventType string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
}

func handleClaudeStreamResponse(w http.ResponseWriter, resp *http.Response, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(NewClaudeError("api_error", "Streaming not supported"))
		return
	}

	msgID := newMessageID()
	headersSent := false
	blockStarted := false
	outputTokens := 0

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event JieKouSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			log.Printf("claude-sse: parse error: %v", err)
			continue
		}

		if errMsg, isErr := parseSSEError(&event); isErr {
			log.Printf("claude-sse: upstream error: %s", errMsg)
			if !headersSent {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				json.NewEncoder(w).Encode(NewClaudeError("api_error", errMsg))
				return
			}
			continue
		}

		switch event.Type {
		case "text-start":
			if !headersSent {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				w.Header().Set("X-Accel-Buffering", "no")
				w.WriteHeader(http.StatusOK)
				headersSent = true

				writeClaudeSSE(w, "message_start", NewClaudeMessageStartEvent(model, msgID))
				flusher.Flush()

				writeClaudeSSE(w, "content_block_start", NewClaudeContentBlockStartEvent())
				flusher.Flush()
				blockStarted = true

				writeClaudeSSE(w, "ping", NewClaudePingEvent())
				flusher.Flush()
			}

		case "text-delta":
			if event.Delta != "" {
				if !headersSent {
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("Cache-Control", "no-cache")
					w.Header().Set("Connection", "keep-alive")
					w.Header().Set("X-Accel-Buffering", "no")
					w.WriteHeader(http.StatusOK)
					headersSent = true

					writeClaudeSSE(w, "message_start", NewClaudeMessageStartEvent(model, msgID))
					flusher.Flush()

					writeClaudeSSE(w, "content_block_start", NewClaudeContentBlockStartEvent())
					flusher.Flush()
					blockStarted = true
				}

				outputTokens++
				writeClaudeSSE(w, "content_block_delta", NewClaudeContentBlockDeltaEvent(event.Delta))
				flusher.Flush()
			}
		}
	}

	if headersSent {
		if blockStarted {
			writeClaudeSSE(w, "content_block_stop", NewClaudeContentBlockStopEvent())
			flusher.Flush()
		}

		writeClaudeSSE(w, "message_delta", NewClaudeMessageDeltaEvent(outputTokens))
		flusher.Flush()

		writeClaudeSSE(w, "message_stop", NewClaudeMessageStopEvent())
		flusher.Flush()
	}

	if err := scanner.Err(); err != nil {
		log.Printf("claude-sse: stream read error: %v", err)
	}
}

func handleClaudeNonStreamResponse(w http.ResponseWriter, resp *http.Response, model string) {
	msgID := newMessageID()
	var fullContent strings.Builder

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event JieKouSSEEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		if errMsg, isErr := parseSSEError(&event); isErr {
			log.Printf("claude-sse: upstream error in non-stream: %s", errMsg)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(NewClaudeError("api_error", errMsg))
			return
		}

		if event.Type == "text-delta" && event.Delta != "" {
			fullContent.WriteString(event.Delta)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("claude-sse: non-stream read error: %v", err)
	}

	result := NewClaudeNonStreamResponse(model, fullContent.String(), msgID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}
