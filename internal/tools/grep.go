package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"time"
)

const DefaultGrepLimit = 200

type GrepTool struct {
	Workspace Workspace
	Limiter   OutputLimiter
}

type grepArguments struct {
	Pattern       string `json:"pattern"`
	Path          string `json:"path"`
	CaseSensitive bool   `json:"case"`
	Gitignore     *bool  `json:"gitignore"`
	Limit         int    `json:"limit"`
	ContextBefore int    `json:"context_before"`
	ContextAfter  int    `json:"context_after"`
}

type grepMatch struct {
	Path   string   `json:"path"`
	Line   int      `json:"line"`
	Text   string   `json:"text"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

func (GrepTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "grep",
		Description: "Search text files in the agent workspace with a regular expression.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"pattern":{"type":"string"},"path":{"type":"string"},"case":{"type":"boolean"},"gitignore":{"type":"boolean"},"limit":{"type":"integer","minimum":1},"context_before":{"type":"integer","minimum":0},"context_after":{"type":"integer","minimum":0}},"required":["pattern"]}`),
		Policy: ToolPolicy{
			Tier:        TierRead,
			Timeout:     30 * time.Second,
			Concurrency: ConcurrencyShared,
		},
		Decode: decodeGrepArguments,
	}
}

func decodeGrepArguments(raw json.RawMessage) (any, error) {
	var arguments grepArguments
	if err := decodeJSON(raw, &arguments); err != nil {
		return nil, fmt.Errorf("tools: decode grep arguments: %w", err)
	}
	if strings.TrimSpace(arguments.Pattern) == "" {
		return nil, fmt.Errorf("tools: grep pattern is required")
	}
	if arguments.Limit < 0 || arguments.ContextBefore < 0 || arguments.ContextAfter < 0 {
		return nil, fmt.Errorf("tools: grep limits cannot be negative")
	}
	return arguments, nil
}

func (tool GrepTool) Execute(ctx context.Context, execution ExecutionContext, arguments any) (ToolResult, error) {
	args, ok := arguments.(grepArguments)
	if !ok {
		return ToolResult{}, fmt.Errorf("tools: grep arguments have type %T", arguments)
	}
	workspace, err := resolveToolWorkspace(tool.Workspace, execution.Session)
	if err != nil {
		return ToolResult{}, err
	}
	pattern := args.Pattern
	if !args.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	expression, err := regexp.Compile(pattern)
	if err != nil {
		return ToolResult{}, fmt.Errorf("tools: invalid grep pattern: %w", err)
	}
	rootInput := args.Path
	if strings.TrimSpace(rootInput) == "" {
		rootInput = "."
	}
	resolved, err := workspace.Resolve(rootInput)
	if err != nil {
		return ToolResult{}, err
	}
	info, err := os.Stat(resolved.Absolute)
	if err != nil {
		return ToolResult{}, err
	}
	limit := args.Limit
	if limit <= 0 {
		limit = DefaultGrepLimit
	}
	gitignore := true
	if args.Gitignore != nil {
		gitignore = *args.Gitignore
	}
	matches := make([]grepMatch, 0, limit)
	visit := func(absolute, relative string, entry fs.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(absolute)
		if err != nil || isBinary(data) {
			return nil
		}
		lines := splitLines(strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(string(data), "\ufeff"), "\r\n", "\n"), "\r", "\n"))
		for lineIndex, line := range lines {
			if !expression.MatchString(line) {
				continue
			}
			beforeStart := lineIndex - args.ContextBefore
			if beforeStart < 0 {
				beforeStart = 0
			}
			afterEnd := lineIndex + args.ContextAfter + 1
			if afterEnd > len(lines) {
				afterEnd = len(lines)
			}
			before := append([]string(nil), lines[beforeStart:lineIndex]...)
			after := append([]string(nil), lines[lineIndex+1:afterEnd]...)
			matches = append(matches, grepMatch{Path: relative, Line: lineIndex + 1, Text: line, Before: before, After: after})
			if len(matches) >= limit {
				return errGrepLimit
			}
		}
		return nil
	}
	if info.IsDir() {
		walkErr := walkWorkspace(ctx, workspace, resolved.Absolute, walkOptions{Gitignore: gitignore}, visit)
		if walkErr != nil && walkErr != errGrepLimit {
			return ToolResult{}, walkErr
		}
	} else {
		if err := visit(resolved.Absolute, resolved.Relative, fs.FileInfoToDirEntry(info)); err != nil && err != errGrepLimit {
			return ToolResult{}, err
		}
	}
	lines := formatGrepMatches(matches)
	output := strings.Join(lines, "\n")
	limited, err := tool.Limiter.Apply(args.Pattern, []byte(output))
	if err != nil {
		return ToolResult{}, err
	}
	details := map[string]any{
		"pattern":   args.Pattern,
		"matches":   matches,
		"limit":     limit,
		"truncated": len(matches) >= limit || limited.Truncated,
	}
	if limited.Artifact != nil {
		details["artifact"] = *limited.Artifact
	}
	return ToolResult{Content: limited.Text, Details: details, Metadata: ResultMetadata{
		OutputBytes: limited.Bytes,
		Truncated:   details["truncated"].(bool),
	}}, nil
}

func formatGrepMatches(matches []grepMatch) []string {
	lines := make([]string, 0, len(matches))
	for _, match := range matches {
		for index, line := range match.Before {
			lines = append(lines, fmt.Sprintf("%s:%d-: %s", match.Path, match.Line-len(match.Before)+index, line))
		}
		lines = append(lines, fmt.Sprintf("%s:%d: %s", match.Path, match.Line, match.Text))
		for index, line := range match.After {
			lines = append(lines, fmt.Sprintf("%s:%d+: %s", match.Path, match.Line+index+1, line))
		}
	}
	return lines
}

var errGrepLimit = fmt.Errorf("tools: grep result limit reached")
