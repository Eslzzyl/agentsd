package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceRejectsPathsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := workspace.Resolve("src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Relative != "src/main.go" || !strings.HasPrefix(resolved.Absolute, workspace.Root) {
		t.Fatalf("resolved = %#v", resolved)
	}
	if _, err := workspace.Resolve("../outside.txt"); err == nil {
		t.Fatal("outside path was accepted")
	}
}

func TestOutputLimiterStoresCompleteArtifact(t *testing.T) {
	store, err := NewArtifactStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	limited, err := (OutputLimiter{MaxBytes: 4, Store: store}).Apply("grep", []byte("abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if limited.Text != "abcd" || !limited.Truncated || limited.Artifact == nil || limited.Bytes != 6 {
		t.Fatalf("limited = %#v", limited)
	}
	stored, err := os.ReadFile(limited.Artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(stored) != "abcdef" {
		t.Fatalf("artifact = %q", stored)
	}
}

func TestReadGlobGrepAndFindTools(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\nneedle first\ntoken exchange\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "helper.go"), []byte("package sub\nneedle second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden.go"), []byte("needle hidden\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	limiter := OutputLimiter{MaxBytes: 1 << 20}

	readResult := executeFileTool(t, ReadTool{Workspace: workspace, Limiter: limiter}, `{"path":"main.go:2-3"}`)
	if !strings.Contains(readResult.Content, "2\tneedle first") || !strings.Contains(readResult.Content, "3\ttoken exchange") {
		t.Fatalf("read result = %q", readResult.Content)
	}

	globResult := executeFileTool(t, GlobTool{Workspace: workspace, Limiter: limiter}, `{"path":"**/*.go"}`)
	if !strings.Contains(globResult.Content, "main.go") || !strings.Contains(globResult.Content, "sub/helper.go") || strings.Contains(globResult.Content, ".hidden.go") {
		t.Fatalf("glob result = %q", globResult.Content)
	}

	grepResult := executeFileTool(t, GrepTool{Workspace: workspace, Limiter: limiter}, `{"pattern":"needle","context_before":1,"context_after":1}`)
	if !strings.Contains(grepResult.Content, "main.go:2: needle first") || !strings.Contains(grepResult.Content, "main.go:1-: package main") {
		t.Fatalf("grep result = %q", grepResult.Content)
	}

	findResult := executeFileTool(t, FindTool{Workspace: workspace, Limiter: limiter}, `{"query":"token exchange","grep_keywords":["token"]}`)
	if !strings.Contains(findResult.Content, "main.go") {
		t.Fatalf("find result = %q", findResult.Content)
	}
}

func TestWriteAndEditToolsUseSnapshots(t *testing.T) {
	root := t.TempDir()
	workspace, err := NewWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	writeResult := executeFileTool(t, WriteTool{Workspace: workspace}, `{"path":"notes.txt","content":"one\ntwo\n"}`)
	if !strings.Contains(writeResult.Content, "wrote notes.txt") {
		t.Fatalf("write result = %q", writeResult.Content)
	}
	snapshot, err := SnapshotFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"path":            "notes.txt",
		"old_string":      "two",
		"new_string":      "TWO",
		"expected_sha256": snapshot.SHA256,
	})
	editResult := executeFileTool(t, EditTool{Workspace: workspace}, string(payload))
	if !strings.Contains(editResult.Content, "updated notes.txt") {
		t.Fatalf("edit result = %q", editResult.Content)
	}
	data, err := os.ReadFile(filepath.Join(root, "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\nTWO\n" {
		t.Fatalf("edited file = %q", data)
	}
	if _, err := executeFileToolResult(EditTool{Workspace: workspace}, `{"path":"notes.txt","old_string":"missing","new_string":"x"}`); err == nil {
		t.Fatal("missing replacement was accepted")
	}
	if _, err := executeFileToolResult(EditTool{Workspace: workspace}, `{"path":"notes.txt","operation":"rename","rename_to":"renamed.txt"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
}

func executeFileTool(t *testing.T, tool Tool, raw string) ToolResult {
	t.Helper()
	result, err := executeFileToolResult(tool, raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func executeFileToolResult(tool Tool, raw string) (ToolResult, error) {
	arguments, err := tool.Spec().Decode(json.RawMessage(raw))
	if err != nil {
		return ToolResult{}, err
	}
	return tool.Execute(context.Background(), ExecutionContext{}, arguments)
}
func TestFirstBatchToolsRegisterWithStableDefinitions(t *testing.T) {
	workspace, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	limiter := OutputLimiter{MaxBytes: 1024}
	registry, err := NewRegistry(
		ReadTool{Workspace: workspace, Limiter: limiter},
		GlobTool{Workspace: workspace, Limiter: limiter},
		GrepTool{Workspace: workspace, Limiter: limiter},
		FindTool{Workspace: workspace, Limiter: limiter},
		WriteTool{Workspace: workspace},
		EditTool{Workspace: workspace},
		ShellTool{WorkingDir: workspace.Root},
	)
	if err != nil {
		t.Fatal(err)
	}
	definitions := registry.Definitions()
	if len(definitions) != 7 {
		t.Fatalf("definitions = %d", len(definitions))
	}
	want := []string{"read", "glob", "grep", "find", "write", "edit", "shell"}
	for index, definition := range definitions {
		if definition.Function.Name != want[index] {
			t.Fatalf("definition[%d] = %q, want %q", index, definition.Function.Name, want[index])
		}
	}
}
