package llm

import (
	"context"
	"encoding/json"
)

// Client sends requests to one model API implementation.
type Client interface {
	Complete(ctx context.Context, req Request) (Response, error)
	Stream(ctx context.Context, req Request, yield func(Event) error) error
}

type APIType string

const (
	APITypeOpenAI          APIType = "openai"
	APITypeOpenAIResponses APIType = "openai_responses"
	APITypeOpenAIWebSocket APIType = "openai_websocket"
	APITypeAnthropic       APIType = "anthropic"
	APITypeGoogle          APIType = "google"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Request struct {
	Model         string
	Messages      []Message
	Tools         []Tool
	ToolChoice    *ToolChoice
	Temperature   *float64
	TopP          *float64
	MaxTokens     *int
	Stop          []string
	ThinkingLevel ThinkingLevelType
	ExtraBody     map[string]any
}

type ToolChoice struct {
	Mode         string
	FunctionName string
}

type Message struct {
	Role       Role
	Content    string
	Images     []Image
	Name       string
	ToolCallID string
	ToolCalls  []ToolCall
}

// Image contains image bytes for image-capable model APIs.
// MIMEType may be empty; providers can detect it from Data.
type Image struct {
	Data     []byte
	MIMEType string
	Detail   ImageDetail
}

type ImageDetail string

const (
	ImageDetailAuto ImageDetail = "auto"
	ImageDetailLow  ImageDetail = "low"
	ImageDetailHigh ImageDetail = "high"
)

type Tool struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type FunctionDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Response struct {
	ID           string
	Model        string
	Choices      []Choice
	Usage        Usage
	UsagePresent bool
}

type Choice struct {
	Index        int
	Message      Message
	FinishReason string
}

type Usage struct {
	Input      int `json:"input"`       // non-cached input tokens
	Output     int `json:"output"`      // output tokens
	CacheRead  int `json:"cache_read"`  // tokens read from prompt cache
	CacheWrite int `json:"cache_write"` // tokens written to prompt cache
	Total      int `json:"total"`       // input + cache read/write + output
}

func (usage Usage) PromptTokens() int {
	return usage.Input + usage.CacheRead + usage.CacheWrite
}

func (usage Usage) Add(other Usage) Usage {
	return Usage{
		Input:      usage.Input + other.Input,
		Output:     usage.Output + other.Output,
		CacheRead:  usage.CacheRead + other.CacheRead,
		CacheWrite: usage.CacheWrite + other.CacheWrite,
		Total:      usage.Total + other.Total,
	}
}

type EventType string

const (
	EventTextDelta     EventType = "text_delta"
	EventToolCallDelta EventType = "tool_call_delta"
	EventCompleted     EventType = "completed"
	EventUsage         EventType = "usage"
)

type Event struct {
	Type         EventType
	ResponseID   string
	Model        string
	ChoiceIndex  int
	Text         string
	ToolCall     *ToolCallDelta
	FinishReason string
	Usage        *Usage
}

type ToolCallDelta struct {
	Index     int
	ID        string
	Type      string
	Name      string
	Arguments string
}
