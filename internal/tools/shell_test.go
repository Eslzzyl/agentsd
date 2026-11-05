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
	content, err := tool.Execute(context.Background(), json.RawMessage(`{"command":"`+command+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var result shellResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.ExitCode != 0 || !strings.Contains(result.Stdout, "shell-test") {
		t.Fatalf("result = %#v", result)
	}
}

func TestShellToolReturnsNonZeroExitCode(t *testing.T) {
	command := "exit 7"
	if runtime.GOOS == "windows" {
		command = "exit /B 7"
	}
	content, err := (ShellTool{}).Execute(context.Background(), json.RawMessage(`{"command":"`+command+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var result shellResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		t.Fatal(err)
	}
	if result.OK || result.ExitCode != 7 {
		t.Fatalf("result = %#v", result)
	}
}
