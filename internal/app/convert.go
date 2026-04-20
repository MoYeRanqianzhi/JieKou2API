package app

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// OpenAI request types

type OpenAIRequest struct {
	Model            string         `json:"model"`
	Messages         []OpenAIMsg    `json:"messages"`
	MaxTokens        *int           `json:"max_tokens,omitempty"`
	Temperature      *float64       `json:"temperature,omitempty"`
	Stream           *bool          `json:"stream,omitempty"`
	Tools            []any          `json:"tools,omitempty"`
	PresencePenalty  *float64       `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64       `json:"frequency_penalty,omitempty"`
}

type OpenAIMsg struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // string or []ContentPart
}

// JieKou request types

type JieKouRequest struct {
	SystemContent    string            `json:"system_content"`
	ResponseFormat   map[string]string `json:"response_format"`
	MaxTokens        int               `json:"max_tokens"`
	Temperature      float64           `json:"temperature"`
	PresencePenalty  float64           `json:"presence_penalty"`
	FrequencyPenalty float64           `json:"frequency_penalty"`
	MinP             float64           `json:"min_p"`
	TopK             int               `json:"top_k"`
	Model            string            `json:"model"`
	Tools            []any             `json:"tools"`
	EnableThinking   bool              `json:"enable_thinking"`
	IsReasoningModel bool              `json:"isReasoningModel"`
	ID               string            `json:"id"`
	Messages         []JieKouMsg       `json:"messages"`
	Trigger          string            `json:"trigger"`
}

type JieKouMsg struct {
	Parts []JieKouPart `json:"parts"`
	ID    string       `json:"id"`
	Role  string       `json:"role"`
}

type JieKouPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// OpenAI SSE response types

type OpenAIStreamChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Created int64             `json:"created"`
	Model   string            `json:"model"`
	Choices []OpenAISSEChoice `json:"choices"`
}

type OpenAISSEChoice struct {
	Index        int             `json:"index"`
	Delta        OpenAISSEDelta  `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
}

type OpenAISSEDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// OpenAI non-stream response types

type OpenAIResponse struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []OpenAIChoice  `json:"choices"`
	Usage   *OpenAIUsage    `json:"usage,omitempty"`
}

type OpenAIChoice struct {
	Index        int        `json:"index"`
	Message      OpenAIMsg  `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func ConvertRequest(oai *OpenAIRequest, defaultModel string) *JieKouRequest {
	model := oai.Model
	if model == "" {
		model = defaultModel
	}

	systemContent := ""
	var jkMsgs []JieKouMsg

	for _, msg := range oai.Messages {
		if msg.Role == "system" {
			systemContent = extractContent(msg.Content)
			continue
		}
		jkMsgs = append(jkMsgs, JieKouMsg{
			Parts: []JieKouPart{{
				Type: "text",
				Text: extractContent(msg.Content),
			}},
			ID:   uuid.New().String()[:16],
			Role: msg.Role,
		})
	}

	maxTokens := 4096
	if oai.MaxTokens != nil {
		maxTokens = *oai.MaxTokens
	}
	temperature := 1.0
	if oai.Temperature != nil {
		temperature = *oai.Temperature
	}
	presencePenalty := 0.0
	if oai.PresencePenalty != nil {
		presencePenalty = *oai.PresencePenalty
	}
	frequencyPenalty := 0.0
	if oai.FrequencyPenalty != nil {
		frequencyPenalty = *oai.FrequencyPenalty
	}

	tools := oai.Tools
	if tools == nil {
		tools = []any{}
	}

	return &JieKouRequest{
		SystemContent:    systemContent,
		ResponseFormat:   map[string]string{"type": "text"},
		MaxTokens:        maxTokens,
		Temperature:      temperature,
		PresencePenalty:  presencePenalty,
		FrequencyPenalty: frequencyPenalty,
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

func extractContent(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var result string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if t, ok := m["type"].(string); ok && t == "text" {
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

func NewStreamChunk(model, content string, completionID string, finishReason *string) *OpenAIStreamChunk {
	return &OpenAIStreamChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []OpenAISSEChoice{{
			Index: 0,
			Delta: OpenAISSEDelta{
				Content: content,
			},
			FinishReason: finishReason,
		}},
	}
}

func NewStreamRoleChunk(model, completionID string) *OpenAIStreamChunk {
	return &OpenAIStreamChunk{
		ID:      completionID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []OpenAISSEChoice{{
			Index: 0,
			Delta: OpenAISSEDelta{
				Role: "assistant",
			},
			FinishReason: nil,
		}},
	}
}

func NewNonStreamResponse(model, content, completionID string) *OpenAIResponse {
	return &OpenAIResponse{
		ID:      completionID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []OpenAIChoice{{
			Index: 0,
			Message: OpenAIMsg{
				Role:    "assistant",
				Content: content,
			},
			FinishReason: "stop",
		}},
	}
}
