package app

import (
	"fmt"

	"github.com/google/uuid"
)

// Anthropic Messages API request

type ClaudeRequest struct {
	Model       string      `json:"model"`
	MaxTokens   int         `json:"max_tokens"`
	System      any         `json:"system,omitempty"`
	Messages    []ClaudeMsg `json:"messages"`
	Stream      *bool       `json:"stream,omitempty"`
	Temperature *float64    `json:"temperature,omitempty"`
	TopK        *int        `json:"top_k,omitempty"`
	TopP        *float64    `json:"top_p,omitempty"`
	StopSeqs    []string    `json:"stop_sequences,omitempty"`
	Tools       []any       `json:"tools,omitempty"`
	ToolChoice  any         `json:"tool_choice,omitempty"`
	Metadata    any         `json:"metadata,omitempty"`
}

type ClaudeMsg struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// Anthropic Messages API streaming SSE events

type ClaudeMessageStart struct {
	Type    string       `json:"type"`
	Message ClaudeMessage `json:"message"`
}

type ClaudeMessage struct {
	ID           string              `json:"id"`
	Type         string              `json:"type"`
	Role         string              `json:"role"`
	Content      []ClaudeContentBlock `json:"content"`
	Model        string              `json:"model"`
	StopReason   *string             `json:"stop_reason"`
	StopSequence *string             `json:"stop_sequence"`
	Usage        ClaudeUsage         `json:"usage"`
}

type ClaudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ClaudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type ClaudeContentBlockStart struct {
	Type         string             `json:"type"`
	Index        int                `json:"index"`
	ContentBlock ClaudeContentBlock `json:"content_block"`
}

type ClaudeContentBlockDelta struct {
	Type  string      `json:"type"`
	Index int         `json:"index"`
	Delta ClaudeDelta `json:"delta"`
}

type ClaudeDelta struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ClaudeContentBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type ClaudeMessageDelta struct {
	Type  string           `json:"type"`
	Delta ClaudeStopDelta  `json:"delta"`
	Usage ClaudeUsage      `json:"usage"`
}

type ClaudeStopDelta struct {
	StopReason   string  `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence"`
}

type ClaudeMessageStop struct {
	Type string `json:"type"`
}

type ClaudePing struct {
	Type string `json:"type"`
}

// Non-streaming response

type ClaudeNonStreamResponse struct {
	ID           string              `json:"id"`
	Type         string              `json:"type"`
	Role         string              `json:"role"`
	Content      []ClaudeContentBlock `json:"content"`
	Model        string              `json:"model"`
	StopReason   string              `json:"stop_reason"`
	StopSequence *string             `json:"stop_sequence"`
	Usage        ClaudeUsage         `json:"usage"`
}

// Error response

type ClaudeErrorResponse struct {
	Type  string           `json:"type"`
	Error ClaudeErrorDetail `json:"error"`
}

type ClaudeErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func ConvertClaudeRequest(req *ClaudeRequest, defaultModel string) *JieKouRequest {
	model := req.Model
	if model == "" {
		model = defaultModel
	}

	systemContent := extractClaudeSystem(req.System)

	var jkMsgs []JieKouMsg
	for _, msg := range req.Messages {
		jkMsgs = append(jkMsgs, JieKouMsg{
			Parts: []JieKouPart{{
				Type: "text",
				Text: extractClaudeContent(msg.Content),
			}},
			ID:   uuid.New().String()[:16],
			Role: msg.Role,
		})
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	temperature := 1.0
	if req.Temperature != nil {
		temperature = *req.Temperature
	}

	tools := make([]any, 0)
	if req.Tools != nil {
		tools = req.Tools
	}

	return &JieKouRequest{
		SystemContent:    systemContent,
		ResponseFormat:   map[string]string{"type": "text"},
		MaxTokens:        maxTokens,
		Temperature:      temperature,
		PresencePenalty:  0,
		FrequencyPenalty: 0,
		MinP:             0,
		TopK:             50,
		Model:            model,
		Tools:            tools,
		EnableThinking:   false,
		IsReasoningModel: false,
		ID:               "jiekou2api-" + uuid.New().String()[:8],
		Messages:         jkMsgs,
		Trigger:          "submit-message",
	}
}

func extractClaudeSystem(system any) string {
	if system == nil {
		return ""
	}
	switch v := system.(type) {
	case string:
		return v
	case []any:
		var result string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "text" {
					if text, ok := m["text"].(string); ok {
						result += text
					}
				}
			}
		}
		return result
	default:
		return fmt.Sprintf("%v", system)
	}
}

func extractClaudeContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var result string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, _ := m["type"].(string); t == "text" {
					if text, ok := m["text"].(string); ok {
						result += text
					}
				}
			}
		}
		return result
	default:
		return fmt.Sprintf("%v", content)
	}
}

func newMessageID() string {
	return "msg_" + uuid.New().String()[:24]
}

func NewClaudeMessageStartEvent(model string, msgID string) *ClaudeMessageStart {
	return &ClaudeMessageStart{
		Type: "message_start",
		Message: ClaudeMessage{
			ID:           msgID,
			Type:         "message",
			Role:         "assistant",
			Content:      []ClaudeContentBlock{},
			Model:        model,
			StopReason:   nil,
			StopSequence: nil,
			Usage:        ClaudeUsage{InputTokens: 0, OutputTokens: 0},
		},
	}
}

func NewClaudeContentBlockStartEvent() *ClaudeContentBlockStart {
	return &ClaudeContentBlockStart{
		Type:         "content_block_start",
		Index:        0,
		ContentBlock: ClaudeContentBlock{Type: "text", Text: ""},
	}
}

func NewClaudePingEvent() *ClaudePing {
	return &ClaudePing{Type: "ping"}
}

func NewClaudeContentBlockDeltaEvent(text string) *ClaudeContentBlockDelta {
	return &ClaudeContentBlockDelta{
		Type:  "content_block_delta",
		Index: 0,
		Delta: ClaudeDelta{Type: "text_delta", Text: text},
	}
}

func NewClaudeContentBlockStopEvent() *ClaudeContentBlockStop {
	return &ClaudeContentBlockStop{
		Type:  "content_block_stop",
		Index: 0,
	}
}

func NewClaudeMessageDeltaEvent(outputTokens int) *ClaudeMessageDelta {
	return &ClaudeMessageDelta{
		Type:  "message_delta",
		Delta: ClaudeStopDelta{StopReason: "end_turn", StopSequence: nil},
		Usage: ClaudeUsage{OutputTokens: outputTokens},
	}
}

func NewClaudeMessageStopEvent() *ClaudeMessageStop {
	return &ClaudeMessageStop{Type: "message_stop"}
}

func NewClaudeNonStreamResponse(model, content, msgID string) *ClaudeNonStreamResponse {
	return &ClaudeNonStreamResponse{
		ID:         msgID,
		Type:       "message",
		Role:       "assistant",
		Content:    []ClaudeContentBlock{{Type: "text", Text: content}},
		Model:      model,
		StopReason: "end_turn",
		Usage:      ClaudeUsage{InputTokens: 0, OutputTokens: 0},
	}
}

func NewClaudeError(errType, message string) *ClaudeErrorResponse {
	return &ClaudeErrorResponse{
		Type:  "error",
		Error: ClaudeErrorDetail{Type: errType, Message: message},
	}
}

