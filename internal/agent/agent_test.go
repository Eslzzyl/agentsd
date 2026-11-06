package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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

type testMessageQueue struct {
	messages []tools.QueuedMessage
}

func (queue *testMessageQueue) Take(mode tools.MessageMode, limit int) ([]tools.QueuedMessage, error) {
	if limit <= 0 {
		return nil, nil
	}
	for index, message := range queue.messages {
		if message.Mode != mode {
			continue
		}
		queue.messages = append(queue.messages[:index], queue.messages[index+1:]...)
		return []tools.QueuedMessage{message}, nil
	}
	return nil, nil
}

func (queue *testMessageQueue) Wait(context.Context, tools.MessageFilter, time.Duration) (tools.QueuedMessage, bool, error) {
	return tools.QueuedMessage{}, false, nil
}

type runtimeWaitQueue struct {
	messages chan tools.QueuedMessage
}

func (queue *runtimeWaitQueue) Take(tools.MessageMode, int) ([]tools.QueuedMessage, error) {
	return nil, nil
}

func (queue *runtimeWaitQueue) Wait(ctx context.Context, _ tools.MessageFilter, timeout time.Duration) (tools.QueuedMessage, bool, error) {
	var timer *time.Timer
	var timerC <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timerC = timer.C
	}
	select {
	case message := <-queue.messages:
		return message, true, nil
	case <-timerC:
		return tools.QueuedMessage{}, false, nil
	case <-ctx.Done():
		return tools.QueuedMessage{}, false, ctx.Err()
	}
}

type controlClient struct {
	firstResponse chan struct{}
	mainCalls     int
	summaryCalls  int
}

func (client *controlClient) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	if len(request.Messages) > 0 &&
		request.Messages[0].Role == llm.RoleSystem &&
		request.Messages[0].Content == summarySystemPrompt {
		client.summaryCalls++
		return llm.Response{Choices: []llm.Choice{{Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: "manual summary",
		}}}}, nil
	}
	client.mainCalls++
	if client.mainCalls == 1 {
		close(client.firstResponse)
		return llm.Response{Choices: []llm.Choice{{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "control-call",
				Type: "function",
				Function: llm.FunctionCall{
					Name:      "lookup",
					Arguments: `{}`,
				},
			}},
		}}}}, nil
	}
	return llm.Response{Choices: []llm.Choice{{Message: llm.Message{
		Role:    llm.RoleAssistant,
		Content: "done",
	}}}}, nil
}

type steeringClient struct {
	queue    *testMessageQueue
	requests []llm.Request
	calls    int
}

func (client *steeringClient) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	client.requests = append(client.requests, request)
	client.calls++
	if client.calls == 1 {
		client.queue.messages = append(client.queue.messages, tools.QueuedMessage{
			ID:      "steer-1",
			Mode:    tools.MessageModeSteer,
			Content: "change direction",
		})
		return llm.Response{Choices: []llm.Choice{{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: llm.FunctionCall{
					Name:      "lookup",
					Arguments: `{}`,
				},
			}},
		}}}}, nil
	}
	return llm.Response{Choices: []llm.Choice{{Message: llm.Message{
		Role:    llm.RoleAssistant,
		Content: "done",
	}}}}, nil
}

func (client *steeringClient) Stream(context.Context, llm.Request, func(llm.Event) error) error {
	return errors.New("stream is not used")
}

func (client *controlClient) Stream(context.Context, llm.Request, func(llm.Event) error) error {
	return errors.New("stream is not used")
}

type fixedTool struct {
	name       string
	definition llm.Tool
	result     string
	policy     tools.ToolPolicy
	delay      time.Duration
	update     bool
	entered    chan<- struct{}
	release    <-chan struct{}
}

func (tool fixedTool) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:        tool.name,
		Description: tool.definition.Function.Description,
		Parameters:  tool.definition.Function.Parameters,
		Policy:      tool.policy,
		Decode: func(arguments json.RawMessage) (any, error) {
			var value any
			if err := json.Unmarshal(arguments, &value); err != nil {
				return nil, err
			}
			return value, nil
		},
	}
}

func (tool fixedTool) Execute(ctx context.Context, execution tools.ExecutionContext, _ any) (tools.ToolResult, error) {
	if tool.entered != nil {
		tool.entered <- struct{}{}
	}
	if tool.release != nil {
		select {
		case <-tool.release:
		case <-ctx.Done():
			return tools.ToolResult{}, ctx.Err()
		}
	}
	if tool.delay > 0 {
		timer := time.NewTimer(tool.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return tools.ToolResult{}, ctx.Err()
		}
	}
	if tool.update && execution.Update != nil {
		execution.Update(tools.ToolResult{Content: "progress"})
	}
	return tools.ToolResult{Content: tool.result}, nil
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
			Name:        "lookup",
			Description: "Look up a value.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
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

func TestRuntimeInjectsSteeringAfterToolBatch(t *testing.T) {
	queue := &testMessageQueue{}
	client := &steeringClient{queue: queue}
	registry, err := tools.NewRegistry(fixedTool{
		name: "lookup",
		definition: llm.Tool{Type: "function", Function: llm.FunctionDefinition{
			Name:        "lookup",
			Description: "Look up a value.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
		result: "ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(client, registry, RuntimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.Run(context.Background(), RunRequest{
		Model:    "test-model",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "run"}},
		Session:  &tools.SessionContext{MessageQueue: queue},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FinalMessage.Content != "done" || len(client.requests) != 2 {
		t.Fatalf("result = %#v, requests = %#v", result, client.requests)
	}
	secondMessages := client.requests[1].Messages
	if len(secondMessages) != 4 {
		t.Fatalf("second request messages = %#v", secondMessages)
	}
	last := secondMessages[len(secondMessages)-1]
	if last.Role != llm.RoleUser || last.Content != "change direction" {
		t.Fatalf("steering message = %#v", last)
	}
}

func TestRuntimeWaitToolWakesOnMessage(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{Choices: []llm.Choice{{Message: llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID:       "wait-1",
				Type:     "function",
				Function: llm.FunctionCall{Name: "wait", Arguments: `{}`},
			}},
		}}}},
		{Choices: []llm.Choice{{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}}}},
	}}
	registry, err := tools.NewRegistry(tools.WaitTool{})
	if err != nil {
		t.Fatal(err)
	}
	queue := &runtimeWaitQueue{messages: make(chan tools.QueuedMessage, 1)}
	waiting := make(chan struct{})
	runtime, err := NewRuntime(client, registry, RuntimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	resultCh := make(chan struct {
		result RunResult
		err    error
	}, 1)
	go func() {
		result, runErr := runtime.Run(context.Background(), RunRequest{
			Model:    "test-model",
			Messages: []llm.Message{{Role: llm.RoleUser, Content: "wait for input"}},
			Session: &tools.SessionContext{
				MessageQueue: queue,
				OnWaiting: func(value bool) {
					if value {
						close(waiting)
					}
				},
			},
		})
		resultCh <- struct {
			result RunResult
			err    error
		}{result: result, err: runErr}
	}()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("wait tool did not start waiting")
	}
	queue.messages <- tools.QueuedMessage{ID: "wait-message", Mode: tools.MessageModeSteer, Content: "message arrived"}
	outcome := <-resultCh
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if outcome.result.FinalMessage.Content != "done" || len(outcome.result.Messages) != 4 {
		t.Fatalf("result = %#v", outcome.result)
	}
	if !strings.Contains(outcome.result.Messages[2].Content, "message arrived") {
		t.Fatalf("wait result = %#v", outcome.result.Messages[2])
	}
}

func TestRuntimeProcessesControlAtTurnBoundary(t *testing.T) {
	client := &controlClient{firstResponse: make(chan struct{})}
	toolRelease := make(chan struct{})
	registry, err := tools.NewRegistry(fixedTool{
		name: "lookup",
		definition: llm.Tool{Type: "function", Function: llm.FunctionDefinition{
			Name:        "lookup",
			Description: "Look up a value.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
		result:  "recent",
		release: toolRelease,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(client, registry, RuntimeOptions{ContextWindow: 1000})
	if err != nil {
		t.Fatal(err)
	}
	control := make(chan ControlRequest, 1)
	controlResult := make(chan ControlResult, 1)
	runDone := make(chan error, 1)
	go func() {
		_, runErr := runtime.Run(context.Background(), RunRequest{
			Model: "test-model",
			Messages: []llm.Message{
				{Role: llm.RoleUser, Content: "old task"},
				{Role: llm.RoleAssistant, Content: "old result"},
				{Role: llm.RoleUser, Content: "continue"},
			},
			Control: control,
		})
		runDone <- runErr
	}()

	select {
	case <-client.firstResponse:
	case <-time.After(time.Second):
		t.Fatal("first model response did not arrive")
	}
	control <- ControlRequest{
		Kind: ControlContextCompact,
		Complete: func(result ControlResult) {
			controlResult <- result
		},
	}
	close(toolRelease)
	select {
	case result := <-controlResult:
		if result.Status != ControlCompleted || result.Compaction == nil {
			t.Fatalf("control result = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("manual compaction did not complete")
	}
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	if client.summaryCalls != 1 {
		t.Fatalf("summary calls = %d, want 1", client.summaryCalls)
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
func TestRuntimeEmitsToolLifecycle(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{Choices: []llm.Choice{{Message: llm.Message{
			ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.FunctionCall{Name: "lookup", Arguments: `{}`}}},
		}}}},
		{Choices: []llm.Choice{{Message: llm.Message{Content: "done"}}}},
	}}
	eventTypes := make([]tools.EventType, 0, 3)
	registry, err := tools.NewRegistry(fixedTool{
		name: "lookup",
		definition: llm.Tool{Type: "function", Function: llm.FunctionDefinition{
			Name:        "lookup",
			Description: "Look up a value.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
		result: "ok",
		update: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(client, registry, RuntimeOptions{
		EventSink: func(event tools.Event) {
			eventTypes = append(eventTypes, event.Type)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Run(context.Background(), RunRequest{Model: "test-model"}); err != nil {
		t.Fatal(err)
	}
	want := []tools.EventType{tools.EventToolStarted, tools.EventToolUpdated, tools.EventToolCompleted}
	if fmt.Sprint(eventTypes) != fmt.Sprint(want) {
		t.Fatalf("event types = %v, want %v", eventTypes, want)
	}
}

func TestRuntimeRunsSharedToolCallsConcurrently(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{Choices: []llm.Choice{{Message: llm.Message{
			ToolCalls: []llm.ToolCall{
				{ID: "call-1", Function: llm.FunctionCall{Name: "lookup", Arguments: `{}`}},
				{ID: "call-2", Function: llm.FunctionCall{Name: "lookup", Arguments: `{}`}},
			},
		}}}},
		{Choices: []llm.Choice{{Message: llm.Message{Content: "done"}}}},
	}}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	registry, err := tools.NewRegistry(fixedTool{
		name: "lookup",
		definition: llm.Tool{Type: "function", Function: llm.FunctionDefinition{
			Name:        "lookup",
			Description: "Look up a value.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
		result:  "ok",
		entered: entered,
		release: release,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(client, registry, RuntimeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	runDone := make(chan error, 1)
	go func() {
		_, runErr := runtime.Run(context.Background(), RunRequest{Model: "test-model"})
		runDone <- runErr
	}()
	for index := 0; index < 2; index++ {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("shared calls did not start together")
		}
	}
	close(release)
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeTurnsToolTimeoutIntoToolResult(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{Choices: []llm.Choice{{Message: llm.Message{
			ToolCalls: []llm.ToolCall{{ID: "call-1", Function: llm.FunctionCall{Name: "lookup", Arguments: `{}`}}},
		}}}},
		{Choices: []llm.Choice{{Message: llm.Message{Content: "done"}}}},
	}}
	registry, err := tools.NewRegistry(fixedTool{
		name: "lookup",
		definition: llm.Tool{Type: "function", Function: llm.FunctionDefinition{
			Name:        "lookup",
			Description: "Look up a value.",
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
		result: "late",
		delay:  time.Second,
		policy: tools.ToolPolicy{Timeout: 10 * time.Millisecond},
	})
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
	if !strings.Contains(result.Messages[1].Content, "timed out") {
		t.Fatalf("tool message = %q", result.Messages[1].Content)
	}
}
