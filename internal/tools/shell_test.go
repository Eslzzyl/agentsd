package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

func TestShellToolReturnsOutputAndExitCode(t *testing.T) {
	command := "printf shell-test"
	if runtime.GOOS == "windows" {
		command = "echo shell-test"
	}
	tool := ShellTool{MaxOutputBytes: 64}
	result, err := executeShellForTest(tool, command)
	if err != nil {
		t.Fatal(err)
	}
	var decoded shellResult
	if err := json.Unmarshal([]byte(result.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.OK || decoded.ExitCode != 0 || !strings.Contains(decoded.Stdout, "shell-test") {
		t.Fatalf("result = %#v", decoded)
	}
	if result.Details == nil || result.Metadata.OutputBytes == 0 {
		t.Fatalf("metadata = %#v", result)
	}
}

func TestShellToolReturnsNonZeroExitCode(t *testing.T) {
	command := "exit 7"
	if runtime.GOOS == "windows" {
		command = "exit /B 7"
	}
	result, err := executeShellForTest(ShellTool{}, command)
	if err != nil {
		t.Fatal(err)
	}
	var decoded shellResult
	if err := json.Unmarshal([]byte(result.Content), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.OK || decoded.ExitCode != 7 || !result.IsError {
		t.Fatalf("result = %#v", result)
	}
}

func executeShellForTest(tool ShellTool, command string) (ToolResult, error) {
	arguments, err := tool.Spec().Decode(json.RawMessage(`{"command":"` + command + `"}`))
	if err != nil {
		return ToolResult{}, err
	}
	return tool.Execute(context.Background(), ExecutionContext{}, arguments)
}
