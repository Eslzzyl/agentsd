package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"agentsd/internal/llm"
	"agentsd/internal/tools"
)

type scriptedClient struct {
	responses []llm.Response
	requests  []llm.Request
}

func (client *scriptedClient) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	client.requests = append(client.requests, request)
	if len(client.responses) == 0 {
		return llm.Response{}, errors.New("script exhausted")
	}
	response := client.responses[0]
	client.responses = client.responses[1:]
	return response, nil
}

func (client *scriptedClient) Stream(context.Context, llm.Request, func(llm.Event) error) error {
	return errors.New("stream is not used")
}

type fixedTool struct {
	name       string
	definition llm.Tool
	result     string
}

func (tool fixedTool) Definition() llm.Tool { return tool.definition }

func (tool fixedTool) Execute(context.Context, json.RawMessage) (string, error) {
	return tool.result, nil
}

func TestRuntimeRunsToolCallsUntilFinalMessage(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{Choices: []llm.Choice{{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: llm.FunctionCall{
					Name:      "lookup",
					Arguments: `{"id":1}`,
				},
			}},
		}}}},
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	lookup, err := tools.NewRegistry(fixedTool{
		name: "lookup",
		definition: llm.Tool{Type: "function", Function: llm.FunctionDefinition{
			Name:       "lookup",
			Parameters: json.RawMessage(`{"type":"object"}`),
		}},
		result: `{"ok":true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(client, lookup, RuntimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), RunRequest{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "run"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage.Content != "done" || result.Turns != 2 {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Messages) != 4 || result.Messages[2].Role != llm.RoleTool || result.Messages[2].ToolCallID != "call-1" {
		t.Fatalf("messages = %#v", result.Messages)
	}
	if len(client.requests) != 2 || len(client.requests[1].Messages) != 3 {
		t.Fatalf("requests = %#v", client.requests)
	}
}

func TestRuntimeUnknownToolBecomesToolMessage(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{Choices: []llm.Choice{{Message: llm.Message{
			ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.FunctionCall{Name: "missing", Arguments: `{}`}}},
		}}}},
		{Choices: []llm.Choice{{Message: llm.Message{Content: "recovered"}}}},
	}}
	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(client, registry, RuntimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), RunRequest{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Messages[1].Content, `"ok":false`) || !strings.Contains(result.Messages[1].Content, "missing") {
		t.Fatalf("tool message = %q", result.Messages[1].Content)
	}
}

func TestRuntimeHonorsExplicitMaxTurns(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.FunctionCall{Name: "missing", Arguments: `{}`}}}}}}},
		{Choices: []llm.Choice{{Message: llm.Message{ToolCalls: []llm.ToolCall{{ID: "call-2", Function: llm.FunctionCall{Name: "missing", Arguments: `{}`}}}}}}},
	}}
	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(client, registry, RuntimeOptions{MaxTurns: 1})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), RunRequest{Model: "test-model"})
	if !errors.Is(err, ErrMaxTurns) || result.Turns != 1 || len(client.requests) != 1 {
		t.Fatalf("result = %#v, err = %v, requests = %d", result, err, len(client.requests))
	}
}
func TestRuntimeDefaultMaxTurnsIsUnlimited(t *testing.T) {
	const toolTurns = 17
	responses := make([]llm.Response, 0, toolTurns+1)
	for index := range toolTurns {
		responses = append(responses, llm.Response{Choices: []llm.Choice{{Message: llm.Message{
			ToolCalls: []llm.ToolCall{{
				ID:       fmt.Sprintf("call-%d", index),
				Type:     "function",
				Function: llm.FunctionCall{Name: "missing", Arguments: `{}`},
			}},
		}}}})
	}
	responses = append(responses, llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "done"}}}})

	client := &scriptedClient{responses: responses}
	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(client, registry, RuntimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), RunRequest{Model: "test-model"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Turns != toolTurns+1 || result.FinalMessage.Content != "done" {
		t.Fatalf("result = %#v", result)
	}
}
