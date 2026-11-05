package openai

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"agentsd/internal/llm"
)

type chatRequest struct {
	Model               string             `json:"model"`
	Messages            []chatMessage      `json:"messages"`
	Tools               []llm.Tool         `json:"tools,omitempty"`
	ToolChoice          any                `json:"tool_choice,omitempty"`
	Temperature         *float64           `json:"temperature,omitempty"`
	TopP                *float64           `json:"top_p,omitempty"`
	MaxCompletionTokens *int               `json:"max_completion_tokens,omitempty"`
	Stop                []string           `json:"stop,omitempty"`
	Stream              bool               `json:"stream,omitempty"`
	StreamOptions       *chatStreamOptions `json:"stream_options,omitempty"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       llm.Role       `json:"role"`
	Content    any            `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []llm.ToolCall `json:"tool_calls,omitempty"`
}

type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL    string          `json:"url"`
	Detail llm.ImageDetail `json:"detail,omitempty"`
}

type namedToolChoice struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int         `json:"index"`
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func (c *Client) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if req.Model == "" {
		return llm.Response{}, fmt.Errorf("openai: model is required")
	}
	chatReq, err := toChatRequest(req, false)
	if err != nil {
		return llm.Response{}, err
	}
	body, err := marshalRequest(chatReq)
	if err != nil {
		return llm.Response{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), body)
	if err != nil {
		return llm.Response{}, fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.do(httpReq)
	if err != nil {
		return llm.Response{}, fmt.Errorf("openai: send request: %w", err)
	}
	defer resp.Body.Close()
	if err := checkHTTPStatus(resp); err != nil {
		return llm.Response{}, err
	}

	var decoded chatResponse
	if err := decodeResponse(resp.Body, &decoded); err != nil {
		return llm.Response{}, err
	}
	result := llm.Response{ID: decoded.ID, Model: decoded.Model}
	result.Choices = make([]llm.Choice, 0, len(decoded.Choices))
	for _, choice := range decoded.Choices {
		content, ok := choice.Message.Content.(string)
		if !ok && choice.Message.Content != nil {
			return llm.Response{}, fmt.Errorf("openai: response message content has type %T, want string", choice.Message.Content)
		}
		result.Choices = append(result.Choices, llm.Choice{
			Index: choice.Index,
			Message: llm.Message{
				Role:       choice.Message.Role,
				Content:    content,
				Name:       choice.Message.Name,
				ToolCallID: choice.Message.ToolCallID,
				ToolCalls:  choice.Message.ToolCalls,
			},
			FinishReason: choice.FinishReason,
		})
	}
	if decoded.Usage != nil {
		result.Usage = llm.Usage{
			PromptTokens:     decoded.Usage.PromptTokens,
			CompletionTokens: decoded.Usage.CompletionTokens,
			TotalTokens:      decoded.Usage.TotalTokens,
		}
	}
	return result, nil
}

func toChatRequest(req llm.Request, stream bool) (chatRequest, error) {
	messages := make([]chatMessage, 0, len(req.Messages))
	for _, message := range req.Messages {
		content := any(message.Content)
		if len(message.Images) > 0 {
			parts := make([]chatContentPart, 0, len(message.Images)+1)
			if message.Content != "" {
				parts = append(parts, chatContentPart{Type: "text", Text: message.Content})
			}
			for _, image := range message.Images {
				dataURL, err := imageDataURL(image)
				if err != nil {
					return chatRequest{}, err
				}
				parts = append(parts, chatContentPart{
					Type: "image_url",
					ImageURL: &chatImageURL{
						URL:    dataURL,
						Detail: image.Detail,
					},
				})
			}
			content = parts
		}
		messages = append(messages, chatMessage{
			Role:       message.Role,
			Content:    content,
			Name:       message.Name,
			ToolCallID: message.ToolCallID,
			ToolCalls:  message.ToolCalls,
		})
	}
	result := chatRequest{
		Model:               req.Model,
		Messages:            messages,
		Tools:               req.Tools,
		ToolChoice:          toChatToolChoice(req.ToolChoice),
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		MaxCompletionTokens: req.MaxTokens,
		Stop:                req.Stop,
		Stream:              stream,
	}
	if stream {
		result.StreamOptions = &chatStreamOptions{IncludeUsage: true}
	}
	return result, nil
}

func imageDataURL(image llm.Image) (string, error) {
	if len(image.Data) == 0 {
		return "", fmt.Errorf("openai: image data is empty")
	}

	mediaType := strings.TrimSpace(image.MIMEType)
	if mediaType == "" {
		mediaType = http.DetectContentType(image.Data)
	} else {
		parsed, _, err := mime.ParseMediaType(mediaType)
		if err != nil {
			return "", fmt.Errorf("openai: invalid image MIME type %q: %w", image.MIMEType, err)
		}
		mediaType = parsed
	}
	mediaType = strings.ToLower(mediaType)
	switch mediaType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return "", fmt.Errorf("openai: unsupported image MIME type %q", mediaType)
	}

	switch image.Detail {
	case "", llm.ImageDetailAuto, llm.ImageDetailLow, llm.ImageDetailHigh:
	default:
		return "", fmt.Errorf("openai: unsupported image detail %q", image.Detail)
	}

	encoded := base64.StdEncoding.EncodeToString(image.Data)
	return "data:" + mediaType + ";base64," + encoded, nil
}

func toChatToolChoice(choice *llm.ToolChoice) any {
	if choice == nil {
		return nil
	}
	if choice.FunctionName != "" {
		named := namedToolChoice{Type: "function"}
		named.Function.Name = choice.FunctionName
		return named
	}
	if choice.Mode != "" {
		return choice.Mode
	}
	return nil
}
