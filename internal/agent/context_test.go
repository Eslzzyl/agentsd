package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"agentsd/internal/llm"
	"agentsd/internal/tools"
)

type compactionTestClient struct {
	mu           sync.Mutex
	mainTurns    int
	summaryCalls int
	requests     []llm.Request
}

func responseWithUsage(response llm.Response) llm.Response {
	response.Usage = llm.Usage{Input: 10, Output: 2, CacheRead: 3, CacheWrite: 1, Total: 16}
	response.UsagePresent = true
	return response
}

func (client *compactionTestClient) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	client.mu.Lock()
	client.requests = append(client.requests, request)
	defer client.mu.Unlock()
	if len(request.Messages) > 0 && request.Messages[0].Role == llm.RoleSystem && request.Messages[0].Content == summarySystemPrompt {
		client.summaryCalls++
		return responseWithUsage(llm.Response{Choices: []llm.Choice{{Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: fmt.Sprintf("summary-%d", client.summaryCalls),
		}}}}), nil
	}
	client.mainTurns++
	if client.mainTurns <= 14 {
		return responseWithUsage(llm.Response{Choices: []llm.Choice{{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   fmt.Sprintf("call-%d", client.mainTurns),
				Type: "function",
				Function: llm.FunctionCall{
					Name:      "lookup",
					Arguments: `{}`,
				},
			}},
		}}}}), nil
	}
	return responseWithUsage(llm.Response{Choices: []llm.Choice{{Message: llm.Message{
		Role:    llm.RoleAssistant,
		Content: "done",
	}}}}), nil
}

func (client *compactionTestClient) Stream(context.Context, llm.Request, func(llm.Event) error) error {
	return fmt.Errorf("stream is not used")
}

func TestContextBudgetDerivesCompactionValuesFromWindow(t *testing.T) {
	budget := newContextBudget(100000)
	if budget.trigger != 80000 || budget.recovery != 64000 || budget.lead != 10000 || budget.keepRecent != 16000 {
		t.Fatalf("budget = %#v", budget)
	}
	if budget.summaryInput != 65000 || budget.summaryOutput != 3000 {
		t.Fatalf("summary budget = %#v", budget)
	}
}
func TestContextStateManualCompactionWorksBelowAutomaticTrigger(t *testing.T) {
	client := &compactionTestClient{}
	state := newContextState(client, RunRequest{
		Model: "test-model",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "old task"},
			{Role: llm.RoleAssistant, Content: "old result"},
			{Role: llm.RoleUser, Content: "continue"},
		},
	}, 1000)
	defer state.close()

	event, err := state.manualCompact(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if event.CompactionNumber != 1 || event.RemovedMessages == 0 {
		t.Fatalf("event = %#v", event)
	}
	if client.summaryCalls != 1 {
		t.Fatalf("summary calls = %d, want 1", client.summaryCalls)
	}
}

func TestFindCompactionCutKeepsLatestToolCallGroup(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "task"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.FunctionCall{Name: "lookup", Arguments: `{}`}}}},
		{Role: llm.RoleTool, ToolCallID: "call-1", Content: strings.Repeat("old ", 20)},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-2", Function: llm.FunctionCall{Name: "lookup", Arguments: `{}`}}}},
		{Role: llm.RoleTool, ToolCallID: "call-2", Content: "recent"},
	}
	cut := findCompactionCut(messages, estimateMessages(messages[3:]))
	if cut != 3 {
		t.Fatalf("cut = %d, want 3", cut)
	}
}

func TestRuntimeCompactsMultipleTimesAndPersistsEvents(t *testing.T) {
	client := &compactionTestClient{}
	registry, err := tools.NewRegistry(fixedTool{
		name: "lookup",
		definition: llm.Tool{Type: "function", Function: llm.FunctionDefinition{
			Name:        "lookup",
			Description: "Look up a value.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
		result: strings.Repeat("tool result ", 300),
	})
	if err != nil {
		t.Fatal(err)
	}
	var events []ContextEvent
	runtime, err := NewRuntime(client, registry, RuntimeOptions{
		ContextWindow: 3000,
		ContextEventSink: func(event ContextEvent) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), RunRequest{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleSystem, Content: "system"}, {Role: llm.RoleUser, Content: "task"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage.Content != "done" || result.Turns != 15 {
		t.Fatalf("result = %#v", result)
	}
	if len(events) < 2 {
		t.Fatalf("compaction events = %#v", events)
	}
	for index, event := range events {
		if event.CompactionNumber != index+1 || event.Summary == "" || event.RemovedMessages == 0 {
			t.Fatalf("event[%d] = %#v", index, event)
		}
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.summaryCalls < len(events) {
		t.Fatalf("summary calls = %d, events = %d", client.summaryCalls, len(events))
	}
	expectedRequests := client.mainTurns + client.summaryCalls
	if result.UsageRequests != expectedRequests || result.Usage != (llm.Usage{
		Input:      expectedRequests * 10,
		Output:     expectedRequests * 2,
		CacheRead:  expectedRequests * 3,
		CacheWrite: expectedRequests,
		Total:      expectedRequests * 16,
	}) {
		t.Fatalf("usage = %#v, requests = %d, want %d", result.Usage, result.UsageRequests, expectedRequests)
	}
	for _, request := range client.requests {
		if len(request.Messages) == 0 || request.Messages[0].Role != llm.RoleSystem || request.Messages[0].Content != summarySystemPrompt {
			continue
		}
		if request.ToolChoice == nil || request.ToolChoice.Mode != "none" || len(request.Tools) != 0 {
			t.Fatalf("summary request = %#v", request)
		}
	}
}
