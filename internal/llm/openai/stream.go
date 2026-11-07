package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"agentsd/internal/llm"
)

type chatChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
}

func (c *Client) Stream(ctx context.Context, req llm.Request, yield func(llm.Event) error) error {
	if req.Model == "" {
		return fmt.Errorf("openai: model is required")
	}
	if yield == nil {
		return fmt.Errorf("openai: stream callback is required")
	}
	chatReq, err := toChatRequest(req, true)
	if err != nil {
		return err
	}
	body, err := marshalRequest(chatReq)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), body)
	if err != nil {
		return fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.do(httpReq)
	if err != nil {
		return fmt.Errorf("openai: send request: %w", err)
	}
	defer resp.Body.Close()
	if err := checkHTTPStatus(resp); err != nil {
		return err
	}

	return readSSE(ctx, resp.Body, func(data string) (bool, error) {
		if data == "[DONE]" {
			return true, nil
		}
		var errorEvent struct {
			Error *struct {
				Type    string `json:"type"`
				Code    string `json:"code"`
				Param   string `json:"param"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &errorEvent); err == nil && errorEvent.Error != nil {
			return false, &APIError{
				StatusCode: http.StatusOK,
				Type:       errorEvent.Error.Type,
				Code:       errorEvent.Error.Code,
				Param:      errorEvent.Error.Param,
				Message:    errorEvent.Error.Message,
				RequestID:  resp.Header.Get("x-request-id"),
			}
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return false, fmt.Errorf("openai: decode stream event: %w", err)
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				if err := yield(llm.Event{
					Type:        llm.EventTextDelta,
					ResponseID:  chunk.ID,
					Model:       chunk.Model,
					ChoiceIndex: choice.Index,
					Text:        choice.Delta.Content,
				}); err != nil {
					return false, err
				}
			}
			for _, call := range choice.Delta.ToolCalls {
				delta := &llm.ToolCallDelta{
					Index:     call.Index,
					ID:        call.ID,
					Type:      call.Type,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				}
				if err := yield(llm.Event{
					Type:        llm.EventToolCallDelta,
					ResponseID:  chunk.ID,
					Model:       chunk.Model,
					ChoiceIndex: choice.Index,
					ToolCall:    delta,
				}); err != nil {
					return false, err
				}
			}
			if choice.FinishReason != "" {
				if err := yield(llm.Event{
					Type:         llm.EventCompleted,
					ResponseID:   chunk.ID,
					Model:        chunk.Model,
					ChoiceIndex:  choice.Index,
					FinishReason: choice.FinishReason,
				}); err != nil {
					return false, err
				}
			}
		}
		if chunk.Usage != nil {
			usage := normalizeUsage(*chunk.Usage)
			if err := yield(llm.Event{
				Type:       llm.EventUsage,
				ResponseID: chunk.ID,
				Model:      chunk.Model,
				Usage:      &usage,
			}); err != nil {
				return false, err
			}
		}
		return false, nil
	})
}

func readSSE(ctx context.Context, body io.Reader, consume func(data string) (bool, error)) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 32*1024), 4<<20)

	var data []string
	done := false
	dispatch := func() error {
		if len(data) == 0 {
			return nil
		}
		stop, err := consume(strings.Join(data, "\n"))
		data = data[:0]
		if err != nil {
			return err
		}
		done = stop
		return nil
	}

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Text()
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			if done {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found || field != "data" {
			continue
		}
		data = append(data, strings.TrimPrefix(value, " "))
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("openai: read event stream: %w", err)
	}
	if err := dispatch(); err != nil {
		return err
	}
	if !done {
		return io.ErrUnexpectedEOF
	}
	return nil
}
