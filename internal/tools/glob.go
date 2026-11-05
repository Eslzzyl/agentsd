package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const DefaultGlobLimit = 200

type GlobTool struct {
	Workspace Workspace
	Limiter   OutputLimiter
}

type globArguments struct {
	Path      string `json:"path"`
	Hidden    bool   `json:"hidden"`
	Gitignore *bool  `json:"gitignore"`
	Limit     int    `json:"limit"`
}

func (GlobTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "glob",
		Description: "Find files and directories by a glob pattern in the agent workspace.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"path":{"type":"string"},"hidden":{"type":"boolean"},"gitignore":{"type":"boolean"},"limit":{"type":"integer","minimum":1}}}`),
		Policy: ToolPolicy{
			Tier:        TierRead,
			Timeout:     30 * time.Second,
			Concurrency: ConcurrencyShared,
		},
		Decode: decodeGlobArguments,
	}
}

func decodeGlobArguments(raw json.RawMessage) (any, error) {
	var arguments globArguments
	if err := decodeJSON(raw, &arguments); err != nil {
		return nil, fmt.Errorf("tools: decode glob arguments: %w", err)
	}
	if arguments.Limit < 0 {
		return nil, fmt.Errorf("tools: glob limit cannot be negative")
	}
	return arguments, nil
}

func (tool GlobTool) Execute(ctx context.Context, execution ExecutionContext, arguments any) (ToolResult, error) {
	args, ok := arguments.(globArguments)
	if !ok {
		return ToolResult{}, fmt.Errorf("tools: glob arguments have type %T", arguments)
	}
	workspace, err := resolveToolWorkspace(tool.Workspace, execution.Session)
	if err != nil {
		return ToolResult{}, err
	}
	pattern := strings.TrimSpace(filepath.ToSlash(args.Path))
	if pattern == "" || pattern == "." {
		pattern = "**/*"
	} else if !strings.Contains(pattern, "/") {
		if strings.ContainsAny(pattern, "*?[") {
			pattern = "**/" + pattern
		} else {
			pattern = pattern + "/**/*"
		}
	}
	root, err := resolveGlobRoot(workspace, pattern)
	if err != nil {
		return ToolResult{}, err
	}
	limit := args.Limit
	if limit <= 0 {
		limit = DefaultGlobLimit
	}
	gitignore := true
	if args.Gitignore != nil {
		gitignore = *args.Gitignore
	}
	matches := make([]string, 0, limit)
	walkErr := walkWorkspace(ctx, workspace, root, walkOptions{IncludeHidden: args.Hidden, Gitignore: gitignore}, func(_ string, relative string, entry fs.DirEntry) error {
		if relative == "" {
			return nil
		}
		candidate := relative
		if entry.IsDir() {
			candidate += "/"
		}
		if globMatch(pattern, strings.TrimSuffix(candidate, "/")) {
			matches = append(matches, candidate)
			if len(matches) >= limit {
				return errGlobLimit
			}
		}
		return nil
	})
	if walkErr != nil && walkErr != errGlobLimit {
		return ToolResult{}, walkErr
	}
	sort.Strings(matches)
	output := strings.Join(matches, "\n")
	limited, err := tool.Limiter.Apply(pattern, []byte(output))
	if err != nil {
		return ToolResult{}, err
	}
	details := map[string]any{
		"pattern":   pattern,
		"matches":   len(matches),
		"limit":     limit,
		"truncated": walkErr == errGlobLimit || limited.Truncated,
	}
	if limited.Artifact != nil {
		details["artifact"] = *limited.Artifact
	}
	return ToolResult{Content: limited.Text, Details: details, Metadata: ResultMetadata{
		OutputBytes: limited.Bytes,
		Truncated:   details["truncated"].(bool),
	}}, nil
}

var errGlobLimit = fmt.Errorf("tools: glob result limit reached")
