package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type EditTool struct {
	Workspace Workspace
}

type editArguments struct {
	Path           string           `json:"path"`
	Mode           string           `json:"mode"`
	Operation      string           `json:"operation"`
	OldString      *string          `json:"old_string"`
	NewString      *string          `json:"new_string"`
	ReplaceAll     bool             `json:"replace_all"`
	Content        *string          `json:"content"`
	RenameTo       string           `json:"rename_to"`
	ExpectedSHA256 string           `json:"expected_sha256"`
	Input          string           `json:"input"`
	Edits          []patchEditEntry `json:"edits"`
}

func (arguments editArguments) mode() string {
	if arguments.Mode == "" {
		return "replace"
	}
	return arguments.Mode
}

func (arguments editArguments) operation() string {
	if arguments.Operation == "" {
		return "replace"
	}
	return arguments.Operation
}

func (EditTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "edit",
		Description: "Edit workspace files using exact replacement, structured patch, or hashline anchors.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string"},"mode":{"type":"string","enum":["replace","patch","hashline"]},"operation":{"type":"string","enum":["replace","create","delete","rename"]},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean"},"content":{"type":"string"},"rename_to":{"type":"string"},"expected_sha256":{"type":"string"},"input":{"type":"string"},"edits":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"op":{"type":"string","enum":["create","delete","update"]},"rename":{"type":"string"},"diff":{"type":"string"}}}}}}`),
		Policy: ToolPolicy{
			Tier:          TierWrite,
			Timeout:       30 * time.Second,
			Concurrency:   ConcurrencyExclusive,
			Interruptible: true,
		},
		Decode: decodeEditArguments,
	}
}

func decodeEditArguments(raw json.RawMessage) (any, error) {
	var arguments editArguments
	if err := decodeJSON(raw, &arguments); err != nil {
		return nil, fmt.Errorf("tools: decode edit arguments: %w", err)
	}
	switch arguments.mode() {
	case "replace":
		if strings.TrimSpace(arguments.Path) == "" {
			return nil, fmt.Errorf("tools: edit path is required")
		}
		if arguments.Operation == "" {
			arguments.Operation = "replace"
		}
		switch arguments.Operation {
		case "replace":
			if arguments.OldString == nil || arguments.NewString == nil {
				return nil, fmt.Errorf("tools: replace requires old_string and new_string")
			}
			if *arguments.OldString == "" {
				return nil, fmt.Errorf("tools: old_string cannot be empty")
			}
		case "create":
			if arguments.Content == nil {
				return nil, fmt.Errorf("tools: create requires content")
			}
		case "delete":
		case "rename":
			if strings.TrimSpace(arguments.RenameTo) == "" {
				return nil, fmt.Errorf("tools: rename requires rename_to")
			}
		default:
			return nil, fmt.Errorf("tools: unsupported edit operation %q", arguments.Operation)
		}
	case "patch":
		if strings.TrimSpace(arguments.Path) == "" {
			return nil, fmt.Errorf("tools: patch path is required")
		}
		if len(arguments.Edits) == 0 {
			return nil, fmt.Errorf("tools: patch requires edits")
		}
	case "hashline":
		if strings.TrimSpace(arguments.Input) == "" {
			return nil, fmt.Errorf("tools: hashline requires input")
		}
	default:
		return nil, fmt.Errorf("tools: unsupported edit mode %q", arguments.Mode)
	}
	arguments.Mode = arguments.mode()
	return arguments, nil
}

func (tool EditTool) Execute(ctx context.Context, execution ExecutionContext, arguments any) (ToolResult, error) {
	args, ok := arguments.(editArguments)
	if !ok {
		return ToolResult{}, fmt.Errorf("tools: edit arguments have type %T", arguments)
	}
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	if args.mode() == "patch" {
		return tool.applyPatch(ctx, execution, args)
	}
	if args.mode() == "hashline" {
		return tool.applyHashline(ctx, execution, args)
	}
	workspace, err := resolveToolWorkspace(tool.Workspace, execution.Session)
	if err != nil {
		return ToolResult{}, err
	}
	resolved, err := workspace.Resolve(args.Path)
	if err != nil {
		return ToolResult{}, err
	}
	switch args.operation() {
	case "create":
		return tool.create(ctx, resolved, args)
	case "delete":
		return tool.delete(ctx, resolved, args)
	case "rename":
		return tool.rename(ctx, workspace, resolved, args)
	case "replace":
		return tool.replace(ctx, resolved, args)
	default:
		return ToolResult{}, fmt.Errorf("tools: unsupported edit operation %q", args.Operation)
	}
}

func (EditTool) create(ctx context.Context, resolved ResolvedPath, args editArguments) (ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	if _, err := os.Stat(resolved.Absolute); err == nil {
		return ToolResult{}, fmt.Errorf("tools: file %q already exists", args.Path)
	} else if !os.IsNotExist(err) {
		return ToolResult{}, err
	}
	data := []byte(*args.Content)
	if err := WriteFileAtomic(resolved.Absolute, data, 0o644); err != nil {
		return ToolResult{}, err
	}
	after, err := SnapshotFile(resolved.Absolute)
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Content: fmt.Sprintf("created %s", resolved.Relative), Details: map[string]any{
		"path":   resolved.Relative,
		"op":     "create",
		"bytes":  len(data),
		"sha256": after.SHA256,
	}}, nil
}

func (EditTool) delete(ctx context.Context, resolved ResolvedPath, args editArguments) (ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	before, err := SnapshotFile(resolved.Absolute)
	if err != nil {
		return ToolResult{}, err
	}
	if args.ExpectedSHA256 != "" && !strings.EqualFold(args.ExpectedSHA256, before.SHA256) {
		return ToolResult{}, fmt.Errorf("tools: file %q has changed", args.Path)
	}
	if err := os.Remove(resolved.Absolute); err != nil {
		return ToolResult{}, fmt.Errorf("tools: delete %q: %w", args.Path, err)
	}
	return ToolResult{Content: fmt.Sprintf("deleted %s", resolved.Relative), Details: map[string]any{
		"path":   resolved.Relative,
		"op":     "delete",
		"bytes":  before.Bytes,
		"sha256": before.SHA256,
	}}, nil
}

func (EditTool) rename(ctx context.Context, workspace Workspace, resolved ResolvedPath, args editArguments) (ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	before, err := SnapshotFile(resolved.Absolute)
	if err != nil {
		return ToolResult{}, err
	}
	if args.ExpectedSHA256 != "" && !strings.EqualFold(args.ExpectedSHA256, before.SHA256) {
		return ToolResult{}, fmt.Errorf("tools: file %q has changed", args.Path)
	}
	target, err := workspace.Resolve(args.RenameTo)
	if err != nil {
		return ToolResult{}, err
	}
	if _, err := os.Stat(target.Absolute); err == nil {
		return ToolResult{}, fmt.Errorf("tools: rename target %q already exists", args.RenameTo)
	} else if !os.IsNotExist(err) {
		return ToolResult{}, err
	}
	if err := os.Rename(resolved.Absolute, target.Absolute); err != nil {
		return ToolResult{}, fmt.Errorf("tools: rename %q: %w", args.Path, err)
	}
	return ToolResult{Content: fmt.Sprintf("renamed %s to %s", resolved.Relative, target.Relative), Details: map[string]any{
		"path":      resolved.Relative,
		"rename_to": target.Relative,
		"op":        "rename",
		"sha256":    before.SHA256,
	}}, nil
}

func (EditTool) replace(ctx context.Context, resolved ResolvedPath, args editArguments) (ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return ToolResult{}, err
	}
	before, err := SnapshotFile(resolved.Absolute)
	if err != nil {
		return ToolResult{}, err
	}
	if args.ExpectedSHA256 != "" && !strings.EqualFold(args.ExpectedSHA256, before.SHA256) {
		return ToolResult{}, fmt.Errorf("tools: file %q has changed", args.Path)
	}
	data, _, err := readTextFile(resolved.Absolute)
	if err != nil {
		return ToolResult{}, err
	}
	oldString := *args.OldString
	newString := *args.NewString
	count := strings.Count(string(data), oldString)
	if count == 0 {
		return ToolResult{}, fmt.Errorf("tools: old_string was not found in %q", args.Path)
	}
	if count > 1 && !args.ReplaceAll {
		return ToolResult{}, fmt.Errorf("tools: old_string occurs %d times in %q; set replace_all to true", count, args.Path)
	}
	updated := strings.Replace(string(data), oldString, newString, -1)
	if !args.ReplaceAll {
		updated = strings.Replace(string(data), oldString, newString, 1)
	}
	if err := before.Verify(); err != nil {
		return ToolResult{}, err
	}
	if err := WriteFileAtomic(resolved.Absolute, []byte(updated), before.Mode); err != nil {
		return ToolResult{}, err
	}
	after, err := SnapshotFile(resolved.Absolute)
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Content: fmt.Sprintf("updated %s (%d replacement(s))", resolved.Relative, count), Details: map[string]any{
		"path":          resolved.Relative,
		"op":            "replace",
		"replacements":  count,
		"before_sha256": before.SHA256,
		"sha256":        after.SHA256,
	}}, nil
}
