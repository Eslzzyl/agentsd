package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

const DefaultReadMaxLines = 2000

type ReadTool struct {
	Workspace Workspace
	Limiter   OutputLimiter
	MaxLines  int
}

type readArguments struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

func (ReadTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "read",
		Description: "Read a text file or directory from the agent workspace. Use path selectors such as :10-20 or :raw; format hashline returns content-hash anchors for edit.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string"},"format":{"type":"string","enum":["numbered","hashline"]}},"required":["path"]}`),
		Policy: ToolPolicy{
			Tier:        TierRead,
			Timeout:     30 * time.Second,
			Concurrency: ConcurrencyShared,
		},
		Decode: decodeReadArguments,
	}
}

func decodeReadArguments(raw json.RawMessage) (any, error) {
	var arguments readArguments
	if err := decodeJSON(raw, &arguments); err != nil {
		return nil, fmt.Errorf("tools: decode read arguments: %w", err)
	}
	if strings.TrimSpace(arguments.Path) == "" {
		return nil, fmt.Errorf("tools: read path is required")
	}
	if arguments.Format == "" {
		arguments.Format = "numbered"
	}
	if arguments.Format != "numbered" && arguments.Format != "hashline" {
		return nil, fmt.Errorf("tools: unsupported read format %q", arguments.Format)
	}
	return arguments, nil
}

func (tool ReadTool) Execute(ctx context.Context, execution ExecutionContext, arguments any) (ToolResult, error) {
	args, ok := arguments.(readArguments)
	if !ok {
		return ToolResult{}, fmt.Errorf("tools: read arguments have type %T", arguments)
	}
	workspace, err := resolveToolWorkspace(tool.Workspace, execution.Session)
	if err != nil {
		return ToolResult{}, err
	}
	literalPath := args.Path
	literalSelector := lineSelector{}
	if resolved, resolveErr := workspace.Resolve(args.Path); resolveErr != nil {
		literalPath, literalSelector, err = splitLineSelector(args.Path)
		if err != nil {
			return ToolResult{}, err
		}
	} else if _, statErr := os.Stat(resolved.Absolute); statErr != nil {
		literalPath, literalSelector, err = splitLineSelector(args.Path)
		if err != nil {
			return ToolResult{}, err
		}
	}
	resolved, err := workspace.Resolve(literalPath)
	if err != nil {
		return ToolResult{}, err
	}
	info, err := os.Stat(resolved.Absolute)
	if err != nil {
		return ToolResult{}, fmt.Errorf("tools: read %q: %w", args.Path, err)
	}
	if info.IsDir() {
		return tool.readDirectory(ctx, workspace, resolved.Absolute)
	}
	data, info, err := readTextFile(resolved.Absolute)
	if err != nil {
		return ToolResult{}, err
	}
	snapshot, err := SnapshotFile(resolved.Absolute)
	if err != nil {
		return ToolResult{}, err
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := splitLines(text)
	start, end := literalSelector.StartLine, literalSelector.EndLine
	if literalSelector.Raw {
		start, end = 1, len(lines)
	}
	if start == 0 {
		start = 1
	}
	if end == 0 || end > len(lines) {
		end = len(lines)
	}
	if len(lines) == 0 {
		start, end = 1, 0
	}
	if start > len(lines) && len(lines) > 0 {
		return ToolResult{}, fmt.Errorf("tools: line %d is outside %q", start, args.Path)
	}
	if end < start && len(lines) > 0 {
		return ToolResult{}, fmt.Errorf("tools: invalid line range for %q", args.Path)
	}
	maxLines := tool.MaxLines
	if maxLines <= 0 {
		maxLines = DefaultReadMaxLines
	}
	truncated := false
	if !literalSelector.Raw && end-start+1 > maxLines {
		end = start + maxLines - 1
		truncated = true
	}
	var output string
	if literalSelector.Raw {
		output = text
	} else {
		selected := lines
		if len(lines) > 0 {
			selected = lines[start-1 : end]
		} else {
			selected = nil
		}
		parts := make([]string, 0, len(selected)+1)
		if args.Format == "hashline" {
			parts = append(parts, fmt.Sprintf("[%s#%s]", resolved.Relative, hashlineTag(text)))
		}
		for index, line := range selected {
			if args.Format == "hashline" {
				parts = append(parts, fmt.Sprintf("%d:%s", start+index, line))
			} else {
				parts = append(parts, fmt.Sprintf("%d\t%s", start+index, line))
			}
		}
		output = strings.Join(parts, "\n")
	}
	limited, err := tool.Limiter.Apply(resolved.Relative, []byte(output))
	if err != nil {
		return ToolResult{}, err
	}
	truncated = truncated || limited.Truncated
	if truncated {
		output = limited.Text + fmt.Sprintf("\n[output truncated; total file bytes: %d]", len(data))
	} else {
		output = limited.Text
	}
	details := map[string]any{
		"path":         resolved.Relative,
		"bytes":        info.Size(),
		"lines":        len(lines),
		"start_line":   start,
		"end_line":     end,
		"truncated":    truncated,
		"sha256":       snapshot.SHA256,
		"output_bytes": limited.Bytes,
	}
	if limited.Artifact != nil {
		details["artifact"] = *limited.Artifact
	}
	return ToolResult{Content: output, Details: details, Metadata: ResultMetadata{
		OutputBytes: limited.Bytes,
		Truncated:   truncated,
	}}, nil
}

func (tool ReadTool) readDirectory(ctx context.Context, workspace Workspace, path string) (ToolResult, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return ToolResult{}, fmt.Errorf("tools: read directory: %w", err)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name() < entries[right].Name() })
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return ToolResult{}, err
		}
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		lines = append(lines, name)
	}
	output := strings.Join(lines, "\n")
	limited, err := tool.Limiter.Apply(relativeDisplay(workspace, path), []byte(output))
	if err != nil {
		return ToolResult{}, err
	}
	return ToolResult{Content: limited.Text, Details: map[string]any{
		"path":      relativeDisplay(workspace, path),
		"directory": true,
		"entries":   len(entries),
		"truncated": limited.Truncated,
	}, Metadata: ResultMetadata{OutputBytes: limited.Bytes, Truncated: limited.Truncated}}, nil
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.TrimSuffix(text, "\n")
	return strings.Split(text, "\n")
}
