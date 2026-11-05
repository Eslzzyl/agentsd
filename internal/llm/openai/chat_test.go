package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentsd/internal/llm"
)

func TestCompleteEncodesRequestAndDecodesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request = %s %s, want POST /v1/chat/completions", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		var request struct {
			Model               string `json:"model"`
			MaxCompletionTokens int    `json:"max_completion_tokens"`
			ToolChoice          struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_choice"`
			Tools []struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
			Messages []struct {
				Role      string `json:"role"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if request.Model != "gpt-test" || request.MaxCompletionTokens != 128 {
			t.Errorf("request model/tokens = %q/%d", request.Model, request.MaxCompletionTokens)
		}
		if request.ToolChoice.Type != "function" || request.ToolChoice.Function.Name != "lookup" || len(request.Tools) != 1 || request.Tools[0].Type != "function" || request.Tools[0].Function.Name != "lookup" {
			t.Errorf("tool request fields = %#v, %#v", request.ToolChoice, request.Tools)
		}
		if len(request.Messages) != 2 || request.Messages[1].Role != "assistant" || len(request.Messages[1].ToolCalls) != 1 {
			t.Errorf("request messages = %#v", request.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat-1","model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{\"id\":1}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":12,"completion_tokens":4,"total_tokens":16}}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{APIKey: "secret", BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	maxTokens := 128
	result, err := client.Complete(context.Background(), llm.Request{
		Model:      "gpt-test",
		Messages:   []llm.Message{{Role: llm.RoleUser, Content: "find"}, {Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Type: "function", Function: llm.FunctionCall{Name: "lookup", Arguments: `{"id":1}`}}}}},
		MaxTokens:  &maxTokens,
		Tools:      []llm.Tool{{Type: "function", Function: llm.FunctionDefinition{Name: "lookup", Parameters: json.RawMessage(`{"type":"object"}`)}}},
		ToolChoice: &llm.ToolChoice{FunctionName: "lookup"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "chat-1" || result.Model != "gpt-test" || len(result.Choices) != 1 {
		t.Fatalf("response metadata/choices = %#v", result)
	}
	choice := result.Choices[0]
	if choice.FinishReason != "tool_calls" || len(choice.Message.ToolCalls) != 1 || choice.Message.ToolCalls[0].Function.Name != "lookup" || choice.Message.ToolCalls[0].Function.Arguments != `{"id":1}` {
		t.Fatalf("response choice = %#v", choice)
	}
	if result.Usage != (llm.Usage{PromptTokens: 12, CompletionTokens: 4, TotalTokens: 16}) {
		t.Fatalf("usage = %#v", result.Usage)
	}
}

func TestStreamEmitsTextToolUsageAndCompletionEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("Accept = %q, want text/event-stream", r.Header.Get("Accept"))
		}
		var request struct {
			Stream        bool `json:"stream"`
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if !request.Stream || !request.StreamOptions.IncludeUsage {
			t.Errorf("stream options = %#v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"id":"chat-2","model":"gpt-test","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			"",
			`data: {"id":"chat-2","model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-2","type":"function","function":{"name":"lookup","arguments":"{\"q\""}}]},"finish_reason":null}]}`,
			"",
			`data: {"id":"chat-2","model":"gpt-test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\"x\"}"}}]},"finish_reason":"tool_calls"}]}`,
			"",
			`data: {"id":"chat-2","model":"gpt-test","choices":[],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`,
			"",
			"data: [DONE]",
			"",
		}, "\n")))
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	var events []llm.Event
	err = client.Stream(context.Background(), llm.Request{Model: "gpt-test", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}}, func(event llm.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 {
		t.Fatalf("got %d events: %#v", len(events), events)
	}
	if events[0].Type != llm.EventTextDelta || events[0].Text != "Hello" {
		t.Fatalf("text event = %#v", events[0])
	}
	if events[1].Type != llm.EventToolCallDelta || events[1].ToolCall == nil || events[1].ToolCall.Name != "lookup" || events[1].ToolCall.Arguments != `{"q"` {
		t.Fatalf("first tool delta = %#v", events[1])
	}
	if events[2].Type != llm.EventToolCallDelta || events[2].ToolCall == nil || events[2].ToolCall.Arguments != `:"x"}` {
		t.Fatalf("second tool delta = %#v", events[2])
	}
	if events[3].Type != llm.EventCompleted || events[3].FinishReason != "tool_calls" {
		t.Fatalf("completion event = %#v", events[3])
	}
	if events[4].Type != llm.EventUsage || events[4].Usage == nil || *events[4].Usage != (llm.Usage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11}) {
		t.Fatalf("usage event = %#v", events[4])
	}
}

func TestCompleteReturnsStructuredAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "req-9")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_error","code":"rate_limit","message":"slow down"}}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), llm.Request{Model: "gpt-test"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests || apiErr.Code != "rate_limit" || apiErr.RequestID != "req-9" || apiErr.Message != "slow down" {
		t.Fatalf("API error = %#v", apiErr)
	}
}

func TestStreamReturnsStructuredAPIErrorEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"error\":{\"type\":\"server_error\",\"code\":\"upstream\",\"message\":\"failed\"}}\n\n"))
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Stream(context.Background(), llm.Request{Model: "gpt-test"}, func(llm.Event) error {
		t.Fatal("unexpected event for error response")
		return nil
	})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Code != "upstream" || apiErr.Message != "failed" {
		t.Fatalf("API error = %#v", apiErr)
	}
}

func TestCompleteEncodesImageBytesAsDataURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Content []struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					ImageURL struct {
						URL    string `json:"url"`
						Detail string `json:"detail"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if len(request.Messages) != 1 || len(request.Messages[0].Content) != 2 {
			t.Errorf("message content parts = %#v", request.Messages)
		} else {
			text, image := request.Messages[0].Content[0], request.Messages[0].Content[1]
			if text.Type != "text" || text.Text != "describe this" {
				t.Errorf("text part = %#v", text)
			}
			if image.Type != "image_url" || image.ImageURL.URL != "data:image/png;base64,iVBORw0KGgo=" || image.ImageURL.Detail != "high" {
				t.Errorf("image part = %#v", image)
			}
		}
		_, _ = w.Write([]byte(`{"id":"image-1","model":"gpt-test","choices":[]}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Complete(context.Background(), llm.Request{
		Model: "gpt-test",
		Messages: []llm.Message{{
			Role:    llm.RoleUser,
			Content: "describe this",
			Images: []llm.Image{{
				Data:   []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a},
				Detail: llm.ImageDetailHigh,
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCompleteRejectsUnsupportedImageBytes(t *testing.T) {
	client, err := NewClient(Config{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), llm.Request{
		Model: "gpt-test",
		Messages: []llm.Message{{
			Role:   llm.RoleUser,
			Images: []llm.Image{{Data: []byte("not an image")}},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported image MIME type") {
		t.Fatalf("error = %v, want unsupported image type", err)
	}
}
