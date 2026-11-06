package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// WaitTool parks the model until a mailbox message arrives or the wait expires.
type WaitTool struct{}

type waitArguments struct {
	TimeoutMS int    `json:"timeout_ms"`
	From      string `json:"from"`
}

type waitResponse struct {
	OK       bool           `json:"ok"`
	TimedOut bool           `json:"timed_out,omitempty"`
	Message  *QueuedMessage `json:"message,omitempty"`
}

func (WaitTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "wait",
		Description: "Wait for a message from an operator or another agent. timeout_ms <= 0 waits until a message arrives.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"timeout_ms":{"type":"integer","description":"Maximum wait time in milliseconds; zero waits indefinitely."},
				"from":{"type":"string","description":"Optional sender filter."}
			}
		}`),
		Policy: ToolPolicy{
			Tier:          TierRead,
			Interruptible: true,
		},
		Decode: func(arguments json.RawMessage) (any, error) {
			var decoded waitArguments
			if len(arguments) > 0 && string(arguments) != "null" {
				if err := json.Unmarshal(arguments, &decoded); err != nil {
					return nil, err
				}
			}
			if decoded.TimeoutMS < 0 {
				return nil, fmt.Errorf("timeout_ms cannot be negative")
			}
			return decoded, nil
		},
	}
}

func (WaitTool) Execute(ctx context.Context, execution ExecutionContext, arguments any) (ToolResult, error) {
	if execution.Session.MessageQueue == nil {
		return ToolResult{
			Content: `{"ok":false,"error":"message queue is unavailable"}`,
			IsError: true,
		}, nil
	}
	decoded, ok := arguments.(waitArguments)
	if !ok {
		return ToolResult{}, fmt.Errorf("wait: invalid arguments %T", arguments)
	}
	if execution.Session.OnWaiting != nil {
		execution.Session.OnWaiting(true)
		defer execution.Session.OnWaiting(false)
	}
	if int64(decoded.TimeoutMS) > int64(time.Duration(1<<63-1)/time.Millisecond) {
		return ToolResult{}, fmt.Errorf("timeout_ms is too large")
	}
	timeout := time.Duration(int64(decoded.TimeoutMS) * int64(time.Millisecond))

	message, received, err := execution.Session.MessageQueue.Wait(
		ctx,
		MessageFilter{From: strings.TrimSpace(decoded.From)},
		timeout,
	)
	if err != nil {
		return ToolResult{}, err
	}
	if !received {
		response := waitResponse{OK: true, TimedOut: true}
		encoded, encodeErr := json.Marshal(response)
		if encodeErr != nil {
			return ToolResult{}, encodeErr
		}
		return ToolResult{Content: string(encoded), Details: response}, nil
	}
	response := waitResponse{OK: true, Message: &message}
	encoded, encodeErr := json.Marshal(response)
	if encodeErr != nil {
		return ToolResult{}, encodeErr
	}
	return ToolResult{Content: string(encoded), Details: response}, nil
}
