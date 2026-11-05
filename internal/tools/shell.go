package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"
)

const DefaultShellMaxOutputBytes = 1 << 20

type ShellTool struct {
	WorkingDir     string
	MaxOutputBytes int
}

type shellArguments struct {
	Command string `json:"command"`
}

type shellResult struct {
	OK        bool   `json:"ok"`
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
	Error     string `json:"error,omitempty"`
}

func (ShellTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "shell",
		Description: "Execute a shell command in the agent workspace.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"command":{"type":"string"}},"required":["command"]}`),
		Policy: ToolPolicy{
			Tier:          TierExec,
			Timeout:       5 * time.Minute,
			Concurrency:   ConcurrencyExclusive,
			Interruptible: true,
		},
		Decode: func(arguments json.RawMessage) (any, error) {
			return decodeShellArguments(arguments)
		},
	}
}

func (tool ShellTool) Execute(ctx context.Context, execution ExecutionContext, arguments any) (ToolResult, error) {
	args, ok := arguments.(shellArguments)
	if !ok {
		return ToolResult{}, fmt.Errorf("tools: shell arguments have type %T", arguments)
	}
	if args.Command == "" {
		return ToolResult{}, fmt.Errorf("tools: shell command is required")
	}

	startedAt := time.Now()
	command := shellCommand(ctx, args.Command)
	if tool.WorkingDir != "" {
		command.Dir = tool.WorkingDir
	} else if execution.Session.CWD != "" {
		command.Dir = execution.Session.CWD
	}
	if len(execution.Session.Env) > 0 {
		command.Env = os.Environ()
		for key, value := range execution.Session.Env {
			command.Env = append(command.Env, key+"="+value)
		}
	}
	stdout := &limitedBuffer{limit: tool.outputLimit()}
	stderr := &limitedBuffer{limit: tool.outputLimit()}
	command.Stdout = stdout
	command.Stderr = stderr

	runErr := command.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ToolResult{}, ctxErr
	}

	result := shellResult{
		OK:        runErr == nil,
		ExitCode:  0,
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Truncated: stdout.truncated || stderr.truncated,
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
			result.Error = runErr.Error()
		}
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		return ToolResult{}, fmt.Errorf("tools: encode shell result: %w", err)
	}
	return ToolResult{
		Content: string(encoded),
		Details: result,
		IsError: runErr != nil,
		Metadata: ResultMetadata{
			Duration:    time.Since(startedAt),
			OutputBytes: len(result.Stdout) + len(result.Stderr),
			Truncated:   result.Truncated,
		},
	}, nil
}

func decodeShellArguments(arguments json.RawMessage) (shellArguments, error) {
	var args shellArguments
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return shellArguments{}, fmt.Errorf("tools: decode shell arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return shellArguments{}, fmt.Errorf("tools: decode shell arguments: multiple JSON values")
		}
		return shellArguments{}, fmt.Errorf("tools: decode shell arguments: %w", err)
	}
	return args, nil
}

func (tool ShellTool) outputLimit() int {
	if tool.MaxOutputBytes > 0 {
		return tool.MaxOutputBytes
	}
	return DefaultShellMaxOutputBytes
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		program := os.Getenv("ComSpec")
		if program == "" {
			program = "cmd.exe"
		}
		return exec.CommandContext(ctx, program, "/C", command)
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command)
}

type limitedBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	remaining := buffer.limit - buffer.Len()
	if remaining <= 0 {
		buffer.truncated = true
		return len(data), nil
	}
	if len(data) > remaining {
		_, _ = buffer.Buffer.Write(data[:remaining])
		buffer.truncated = true
		return len(data), nil
	}
	return buffer.Buffer.Write(data)
}
