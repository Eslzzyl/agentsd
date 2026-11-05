package tools

import (
	"context"
	"encoding/json"
	"time"

	"agentsd/internal/llm"
)

// ToolTier classifies the effect of a tool invocation.
type ToolTier string

const (
	TierRead  ToolTier = "read"
	TierWrite ToolTier = "write"
	TierExec  ToolTier = "exec"
)

// ConcurrencyMode controls whether calls to a tool may share a batch.
type ConcurrencyMode string

const (
	ConcurrencyShared    ConcurrencyMode = "shared"
	ConcurrencyExclusive ConcurrencyMode = "exclusive"
)

// ToolPolicy contains execution properties used by the runtime.
type ToolPolicy struct {
	Tier          ToolTier
	Timeout       time.Duration
	Concurrency   ConcurrencyMode
	Interruptible bool
}

func (policy ToolPolicy) normalized() ToolPolicy {
	if policy.Tier == "" {
		policy.Tier = TierExec
	}
	if policy.Concurrency == "" {
		policy.Concurrency = ConcurrencyShared
	}
	return policy
}

// ToolSpec is the model-facing definition plus the typed argument decoder.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Hidden      bool
	Policy      ToolPolicy
	Decode      func(json.RawMessage) (any, error)
}

// ToolFactory creates a tool instance for one session.
type ToolFactory func(SessionContext) (Tool, error)

func (spec ToolSpec) Definition() llm.Tool {
	return llm.Tool{
		Type: "function",
		Function: llm.FunctionDefinition{
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  append(json.RawMessage(nil), spec.Parameters...),
		},
	}
}

// SessionContext carries runtime-owned state into a tool call.
type SessionContext struct {
	ID            string
	CWD           string
	WorkspaceRoot string
	Env           map[string]string
	Values        map[string]any
}

// Call identifies one model-requested tool invocation.
type Call struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ExecutionContext carries call and session data into a tool implementation.
type ExecutionContext struct {
	Session SessionContext
	Call    Call
	Update  UpdateFunc
}

// UpdateFunc publishes an intermediate result while a tool is running.
type UpdateFunc func(ToolResult)

// Tool is an executable function exposed to the model.
type Tool interface {
	Spec() ToolSpec
	Execute(ctx context.Context, execution ExecutionContext, arguments any) (ToolResult, error)
}

// ResultMetadata describes host-side execution details.
type ResultMetadata struct {
	Duration    time.Duration
	OutputBytes int
	Truncated   bool
}

// ToolResult separates model-visible content from host-visible details.
type ToolResult struct {
	Content  string
	Details  any
	IsError  bool
	Metadata ResultMetadata
}

func (result ToolResult) Text() string {
	if result.Content != "" {
		return result.Content
	}
	if result.Details == nil {
		return ""
	}
	encoded, err := json.Marshal(result.Details)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// EventType identifies a tool lifecycle event.
type EventType string

const (
	EventToolStarted   EventType = "tool.started"
	EventToolUpdated   EventType = "tool.updated"
	EventToolCompleted EventType = "tool.completed"
	EventToolFailed    EventType = "tool.failed"
)

// Event reports one tool lifecycle transition.
type Event struct {
	Type       EventType
	Turn       int
	CallID     string
	ToolName   string
	StartedAt  time.Time
	FinishedAt time.Time
	Result     *ToolResult
	Error      string
}

// EventSink receives runtime tool lifecycle events.
type EventSink func(Event)
