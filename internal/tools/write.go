package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type WriteTool struct {
	Workspace Workspace
}

type writeArguments struct {
	Path           string  `json:"path"`
	Content        *string `json:"content"`
	ExpectedSHA256 string  `json:"expected_sha256"`
}

func (WriteTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "write",
		Description: "Create or replace a text file in the agent workspace.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string"},"content":{"type":"string"},"expected_sha256":{"type":"string"}},"required":["path","content"]}`),
		Policy: ToolPolicy{
			Tier:          TierWrite,
			Timeout:       30 * time.Second,
			Concurrency:   ConcurrencyExclusive,
			Interruptible: true,
		},
		Decode: decodeWriteArguments,
	}
}

func decodeWriteArguments(raw json.RawMessage) (any, error) {
	var arguments writeArguments
	if err := decodeJSON(raw, &arguments); err != nil {
		return nil, fmt.Errorf("tools: decode write arguments: %w", err)
	}
	if strings.TrimSpace(arguments.Path) == "" {
		return nil, fmt.Errorf("tools: write path is required")
	}
	if arguments.Content == nil {
		return nil, fmt.Errorf("tools: write content is required")
	}
	return arguments, nil
}

func (tool WriteTool) Execute(ctx context.Context, execution ExecutionContext, arguments any) (ToolResult, error) {
	args, ok := arguments.(writeArguments)
	if !ok {
		return ToolResult{}, fmt.Errorf("tools: write arguments have type %T", arguments)
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	workspace, err := resolveToolWorkspace(tool.Workspace, execution.Session)
	if err != nil {
		return ToolResult{}, err
	}
	resolved, err := workspace.Resolve(args.Path)
	if err != nil {
		return ToolResult{}, err
	}
	var before *FileSnapshot
	if info, statErr := os.Stat(resolved.Absolute); statErr == nil {
		if info.IsDir() {
			return ToolResult{}, fmt.Errorf("tools: write target %q is a directory", args.Path)
		}
		snapshot, snapshotErr := SnapshotFile(resolved.Absolute)
		if snapshotErr != nil {
			return ToolResult{}, snapshotErr
		}
		before = &snapshot
		if args.ExpectedSHA256 != "" && !strings.EqualFold(args.ExpectedSHA256, snapshot.SHA256) {
			return ToolResult{}, fmt.Errorf("tools: write target %q has changed", args.Path)
		}
	} else if !os.IsNotExist(statErr) {
		return ToolResult{}, fmt.Errorf("tools: inspect write target: %w", statErr)
	} else if args.ExpectedSHA256 != "" {
		return ToolResult{}, fmt.Errorf("tools: write target %q does not exist", args.Path)
	}
	mode := os.FileMode(0o644)
	if before != nil {
		mode = before.Mode
	}
	data := []byte(*args.Content)
	if err := WriteFileAtomic(resolved.Absolute, data, mode); err != nil {
		return ToolResult{}, err
	}
	after, err := SnapshotFile(resolved.Absolute)
	if err != nil {
		return ToolResult{}, err
	}
	details := map[string]any{
		"path":    resolved.Relative,
		"bytes":   len(data),
		"created": before == nil,
		"sha256":  after.SHA256,
	}
	return ToolResult{Content: fmt.Sprintf("wrote %s (%d bytes)", resolved.Relative, len(data)), Details: details, Metadata: ResultMetadata{
		OutputBytes: len(data),
	}}, nil
}
