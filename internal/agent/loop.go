package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"agentsd/internal/llm"
	"agentsd/internal/tools"
)

var ErrMaxTurns = errors.New("agent: maximum turns reached")

type RuntimeOptions struct {
	// MaxTurns == 0 enables an unlimited loop. A positive value enables a turn limit.
	MaxTurns         int
	EventSink        tools.EventSink
	ContextWindow    int
	ContextEventSink ContextEventSink
	Session          tools.SessionContext
	MaxToolTimeout   time.Duration
}

type Runtime struct {
	client           llm.Client
	tools            tools.Registry
	maxTurns         int
	eventSink        tools.EventSink
	contextWindow    int
	contextEventSink ContextEventSink
	session          tools.SessionContext
	maxToolTimeout   time.Duration
}

func NewRuntime(client llm.Client, registry tools.Registry, options RuntimeOptions) (*Runtime, error) {
	if client == nil {
		return nil, fmt.Errorf("agent: llm client is required")
	}
	if options.MaxTurns < 0 {
		return nil, fmt.Errorf("agent: max turns cannot be negative")
	}
	if options.MaxToolTimeout < 0 {
		return nil, fmt.Errorf("agent: max tool timeout cannot be negative")
	}
	return &Runtime{
		client:           client,
		tools:            registry,
		maxTurns:         options.MaxTurns,
		eventSink:        options.EventSink,
		contextWindow:    options.ContextWindow,
		contextEventSink: options.ContextEventSink,
		session:          options.Session,
		maxToolTimeout:   options.MaxToolTimeout,
	}, nil
}

type RunRequest struct {
	Model            string
	Messages         []llm.Message
	MaxTokens        *int
	ExtraBody        map[string]any
	Session          *tools.SessionContext
	EventSink        tools.EventSink
	ContextEventSink ContextEventSink
	Control          <-chan ControlRequest
}

type RunResult struct {
	FinalMessage llm.Message
	Messages     []llm.Message
	Turns        int
}

type ControlKind string

const ControlContextCompact ControlKind = "context.compact"

type ControlStatus string

const (
	ControlCompleted ControlStatus = "completed"
	ControlNoChange  ControlStatus = "no_change"
	ControlFailed    ControlStatus = "failed"
)

type ControlResult struct {
	Status     ControlStatus
	Compaction *ContextEvent
	Error      string
}

type ControlRequest struct {
	Kind     ControlKind
	Complete func(ControlResult)
}

func (runtime *Runtime) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	if strings.TrimSpace(request.Model) == "" {
		return RunResult{}, fmt.Errorf("agent: model is required")
	}

	session := runtime.session
	if request.Session != nil {
		session = *request.Session
	}
	definitions := runtime.tools.Definitions()
	eventSink := runtime.eventSink
	if request.EventSink != nil {
		eventSink = request.EventSink
	}
	contextEventSink := runtime.contextEventSink
	if request.ContextEventSink != nil {
		contextEventSink = request.ContextEventSink
	}
	contextState := newContextState(runtime.client, request, runtime.contextWindow)
	contextState.eventSink = contextEventSink
	defer contextState.close()
	events := newEventEmitter(eventSink)
	for turn := 1; ; turn++ {
		runtime.processControls(ctx, contextState, request.Control)
		if runtime.maxTurns > 0 && turn > runtime.maxTurns {
			return RunResult{Messages: contextState.messages(), Turns: turn - 1}, ErrMaxTurns
		}
		if err := contextState.prepare(ctx); err != nil {
			return RunResult{Messages: contextState.messages(), Turns: turn - 1}, err
		}
		response, err := runtime.client.Complete(ctx, llm.Request{
			Model:     modelName(request.Model),
			Messages:  contextState.messages(),
			Tools:     definitions,
			MaxTokens: request.MaxTokens,
			ExtraBody: request.ExtraBody,
		})
		if err != nil {
			return RunResult{Messages: contextState.messages(), Turns: turn - 1}, err
		}
		contextState.observe(response.Usage)
		if len(response.Choices) == 0 {
			return RunResult{Messages: contextState.messages(), Turns: turn}, fmt.Errorf("agent: model response has no choices")
		}

		assistant := response.Choices[0].Message
		if assistant.Role == "" {
			assistant.Role = llm.RoleAssistant
		}
		contextState.append(assistant)
		if len(assistant.ToolCalls) == 0 {
			runtime.processControls(ctx, contextState, request.Control)
			return RunResult{
				FinalMessage: assistant,
				Messages:     contextState.messages(),
				Turns:        turn,
			}, nil
		}

		toolMessages, err := runtime.executeToolBatch(ctx, turn, assistant.ToolCalls, session, events)
		if err != nil {
			return RunResult{Messages: contextState.messages(), Turns: turn}, err
		}
		contextState.appendAll(toolMessages)
	}
}

func (runtime *Runtime) processControls(
	ctx context.Context,
	state *contextState,
	controls <-chan ControlRequest,
) {
	if controls == nil {
		return
	}
	for {
		select {
		case control, ok := <-controls:
			if !ok {
				return
			}
			result := ControlResult{Status: ControlFailed}
			switch control.Kind {
			case ControlContextCompact:
				event, err := state.manualCompact(ctx)
				switch {
				case err == nil:
					result.Status = ControlCompleted
					result.Compaction = &event
				case errors.Is(err, errContextNoProgress):
					result.Status = ControlNoChange
				default:
					result.Error = err.Error()
				}
			default:
				result.Error = fmt.Sprintf("agent: unknown control %q", control.Kind)
			}
			if control.Complete != nil {
				control.Complete(result)
			}
		default:
			return
		}
	}
}

func modelName(model string) string {
	return strings.TrimSpace(model)
}

type batchResult struct {
	message llm.Message
	err     error
}

func (runtime *Runtime) executeToolBatch(
	ctx context.Context,
	turn int,
	calls []llm.ToolCall,
	session tools.SessionContext,
	events *eventEmitter,
) ([]llm.Message, error) {
	results := make([]batchResult, len(calls))
	exclusive := false
	for index, call := range calls {
		if call.ID == "" {
			return nil, fmt.Errorf("agent: tool call %d has no ID", index)
		}
		spec, ok := runtime.tools.Spec(call.Function.Name)
		if ok && spec.Policy.Concurrency == tools.ConcurrencyExclusive {
			exclusive = true
		}
	}

	runOne := func(index int) {
		call := calls[index]
		content, err := runtime.executeTool(ctx, turn, call, session, events)
		results[index] = batchResult{
			message: llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: call.ID,
				Content:    content,
			},
			err: err,
		}
	}

	if exclusive {
		for index := range calls {
			runOne(index)
			if results[index].err != nil {
				return nil, results[index].err
			}
		}
	} else {
		var waitGroup sync.WaitGroup
		waitGroup.Add(len(calls))
		for index := range calls {
			go func(index int) {
				defer waitGroup.Done()
				runOne(index)
			}(index)
		}
		waitGroup.Wait()
		for _, result := range results {
			if result.err != nil {
				return nil, result.err
			}
		}
	}

	messages := make([]llm.Message, len(results))
	for index, result := range results {
		messages[index] = result.message
	}
	return messages, nil
}

func (runtime *Runtime) executeTool(
	ctx context.Context,
	turn int,
	call llm.ToolCall,
	session tools.SessionContext,
	events *eventEmitter,
) (string, error) {
	startedAt := time.Now()
	toolName := call.Function.Name
	toolCall := tools.Call{
		ID:        call.ID,
		Name:      toolName,
		Arguments: json.RawMessage(call.Function.Arguments),
	}
	events.emit(tools.Event{
		Type:      tools.EventToolStarted,
		Turn:      turn,
		CallID:    call.ID,
		ToolName:  toolName,
		StartedAt: startedAt,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		result := runtime.errorResult(toolName, ctxErr.Error(), startedAt)
		runtime.failedEvent(turn, toolCall, startedAt, result, ctxErr.Error(), events)
		return "", ctxErr
	}

	if !runtime.tools.Visible(toolName) {
		result := runtime.errorResult(toolName, fmt.Sprintf("unknown or disabled tool %q", toolName), startedAt)
		runtime.failedEvent(turn, toolCall, startedAt, result, resultError(result), events)
		return result.Text(), nil
	}
	spec, ok := runtime.tools.Spec(toolName)
	if !ok {
		result := runtime.errorResult(toolName, "tool specification is unavailable", startedAt)
		runtime.failedEvent(turn, toolCall, startedAt, result, resultError(result), events)
		return result.Text(), nil
	}
	arguments, err := spec.Decode(toolCall.Arguments)
	if err != nil {
		result := runtime.errorResult(toolName, err.Error(), startedAt)
		runtime.failedEvent(turn, toolCall, startedAt, result, err.Error(), events)
		return result.Text(), nil
	}
	tool, err := runtime.tools.Resolve(toolName, session)
	if err != nil {
		result := runtime.errorResult(toolName, err.Error(), startedAt)
		runtime.failedEvent(turn, toolCall, startedAt, result, err.Error(), events)
		return result.Text(), nil
	}

	timeout := runtime.toolTimeout(spec.Policy.Timeout)
	toolContext := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		toolContext, cancel = context.WithTimeout(ctx, timeout)
	} else {
		toolContext, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	execution := tools.ExecutionContext{
		Session: session,
		Call:    toolCall,
		Update: func(result tools.ToolResult) {
			events.emit(tools.Event{
				Type:       tools.EventToolUpdated,
				Turn:       turn,
				CallID:     call.ID,
				ToolName:   toolName,
				StartedAt:  startedAt,
				FinishedAt: time.Now(),
				Result:     &result,
			})
		},
	}
	result, executeErr := tool.Execute(toolContext, execution, arguments)
	toolContextErr := toolContext.Err()
	if ctxErr := ctx.Err(); ctxErr != nil {
		runtime.failedEvent(turn, toolCall, startedAt, result, ctxErr.Error(), events)
		return "", ctxErr
	}
	if errors.Is(toolContextErr, context.DeadlineExceeded) ||
		errors.Is(executeErr, context.DeadlineExceeded) {
		message := "tool timed out"
		if timeout > 0 {
			message = fmt.Sprintf("tool timed out after %s", timeout)
		}
		result = runtime.errorResult(toolName, message, startedAt)
		runtime.completeEvent(turn, toolCall, startedAt, result, events)
		return result.Text(), nil
	}
	if executeErr != nil {
		result = runtime.errorResult(toolName, executeErr.Error(), startedAt)
		runtime.failedEvent(turn, toolCall, startedAt, result, executeErr.Error(), events)
		return result.Text(), nil
	}

	result.Metadata.Duration = time.Since(startedAt)
	runtime.completeEvent(turn, toolCall, startedAt, result, events)
	return result.Text(), nil
}

func (runtime *Runtime) toolTimeout(toolTimeout time.Duration) time.Duration {
	if runtime.maxToolTimeout > 0 && (toolTimeout == 0 || runtime.maxToolTimeout < toolTimeout) {
		return runtime.maxToolTimeout
	}
	return toolTimeout
}

func (runtime *Runtime) errorResult(toolName, message string, startedAt time.Time) tools.ToolResult {
	return tools.ToolResult{
		Content: encodeToolError(toolName, message),
		Details: map[string]any{
			"ok":    false,
			"tool":  toolName,
			"error": message,
		},
		IsError: true,
		Metadata: tools.ResultMetadata{
			Duration: time.Since(startedAt),
		},
	}
}

func resultError(result tools.ToolResult) string {
	if details, ok := result.Details.(map[string]any); ok {
		if message, ok := details["error"].(string); ok {
			return message
		}
	}
	return "tool execution failed"
}

func (runtime *Runtime) completeEvent(
	turn int,
	call tools.Call,
	startedAt time.Time,
	result tools.ToolResult,
	events *eventEmitter,
) {
	events.emit(tools.Event{
		Type:       tools.EventToolCompleted,
		Turn:       turn,
		CallID:     call.ID,
		ToolName:   call.Name,
		StartedAt:  startedAt,
		FinishedAt: time.Now(),
		Result:     &result,
	})
}

func (runtime *Runtime) failedEvent(
	turn int,
	call tools.Call,
	startedAt time.Time,
	result tools.ToolResult,
	message string,
	events *eventEmitter,
) {
	events.emit(tools.Event{
		Type:       tools.EventToolFailed,
		Turn:       turn,
		CallID:     call.ID,
		ToolName:   call.Name,
		StartedAt:  startedAt,
		FinishedAt: time.Now(),
		Result:     &result,
		Error:      message,
	})
}

type eventEmitter struct {
	sink tools.EventSink
	mu   sync.Mutex
}

func newEventEmitter(sink tools.EventSink) *eventEmitter {
	return &eventEmitter{sink: sink}
}

func (emitter *eventEmitter) emit(event tools.Event) {
	if emitter.sink == nil {
		return
	}
	emitter.mu.Lock()
	defer emitter.mu.Unlock()
	emitter.sink(event)
}

func encodeToolError(toolName, message string) string {
	encoded, err := json.Marshal(struct {
		OK    bool   `json:"ok"`
		Tool  string `json:"tool,omitempty"`
		Error string `json:"error"`
	}{OK: false, Tool: toolName, Error: message})
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"error":%q}`, message)
	}
	return string(encoded)
}
