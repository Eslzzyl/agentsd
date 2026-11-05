package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agentsd/internal/llm"
	"agentsd/internal/tools"
)

var ErrMaxTurns = errors.New("agent: maximum turns reached")

type RuntimeOptions struct {
	// MaxTurns == 0 enables an unlimited loop. A positive value enables a turn limit.
	MaxTurns int
}

type Runtime struct {
	client   llm.Client
	tools    tools.Registry
	maxTurns int
}

func NewRuntime(client llm.Client, registry tools.Registry, options RuntimeOptions) (*Runtime, error) {
	if client == nil {
		return nil, fmt.Errorf("agent: llm client is required")
	}
	if options.MaxTurns < 0 {
		return nil, fmt.Errorf("agent: max turns cannot be negative")
	}
	return &Runtime{client: client, tools: registry, maxTurns: options.MaxTurns}, nil
}

type RunRequest struct {
	Model     string
	Messages  []llm.Message
	MaxTokens *int
	ExtraBody map[string]any
}

type RunResult struct {
	FinalMessage llm.Message
	Messages     []llm.Message
	Turns        int
}

func (runtime *Runtime) Run(ctx context.Context, request RunRequest) (RunResult, error) {
	if strings.TrimSpace(request.Model) == "" {
		return RunResult{}, fmt.Errorf("agent: model is required")
	}

	messages := append([]llm.Message(nil), request.Messages...)
	definitions := runtime.tools.Definitions()
	for turn := 1; ; turn++ {
		if runtime.maxTurns > 0 && turn > runtime.maxTurns {
			return RunResult{Messages: messages, Turns: turn - 1}, ErrMaxTurns
		}
		response, err := runtime.client.Complete(ctx, llm.Request{
			Model:     request.Model,
			Messages:  messages,
			Tools:     definitions,
			MaxTokens: request.MaxTokens,
			ExtraBody: request.ExtraBody,
		})
		if err != nil {
			return RunResult{Messages: messages, Turns: turn - 1}, err
		}
		if len(response.Choices) == 0 {
			return RunResult{Messages: messages, Turns: turn}, fmt.Errorf("agent: model response has no choices")
		}

		assistant := response.Choices[0].Message
		if assistant.Role == "" {
			assistant.Role = llm.RoleAssistant
		}
		messages = append(messages, assistant)
		result := RunResult{FinalMessage: assistant, Messages: messages, Turns: turn}
		if len(assistant.ToolCalls) == 0 {
			return result, nil
		}

		for index, call := range assistant.ToolCalls {
			if call.ID == "" {
				return result, fmt.Errorf("agent: tool call %d has no ID", index)
			}
			content := runtime.executeTool(ctx, call)
			if ctxErr := ctx.Err(); ctxErr != nil {
				return RunResult{Messages: messages, Turns: turn}, ctxErr
			}
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: call.ID,
				Content:    content,
			})
		}
	}
}

func (runtime *Runtime) executeTool(ctx context.Context, call llm.ToolCall) string {
	tool, ok := runtime.tools.Lookup(call.Function.Name)
	if !ok {
		return encodeToolError(call.Function.Name, fmt.Sprintf("unknown tool %q", call.Function.Name))
	}
	content, err := tool.Execute(ctx, json.RawMessage(call.Function.Arguments))
	if err == nil {
		return content
	}
	return encodeToolError(call.Function.Name, err.Error())
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
