package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"
)

const DefaultFindLimit = 50

type FindTool struct {
	Workspace Workspace
	Limiter   OutputLimiter
}

type findArguments struct {
	Query        string   `json:"query"`
	GrepKeywords []string `json:"grep_keywords"`
	Path         string   `json:"path"`
	Limit        int      `json:"limit"`
}

type findCandidate struct {
	Path  string
	Score int
	Line  int
	Text  string
}

func (FindTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "find",
		Description: "Find workspace files by matching words from a behavior description against file names and text.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string"},"grep_keywords":{"type":"array","items":{"type":"string"}},"path":{"type":"string"},"limit":{"type":"integer","minimum":1}},"required":["query","grep_keywords"]}`),
		Policy: ToolPolicy{
			Tier:        TierRead,
			Timeout:     30 * time.Second,
			Concurrency: ConcurrencyShared,
		},
		Decode: decodeFindArguments,
	}
}

func decodeFindArguments(raw json.RawMessage) (any, error) {
	var arguments findArguments
	if err := decodeJSON(raw, &arguments); err != nil {
		return nil, fmt.Errorf("tools: decode find arguments: %w", err)
	}
	if strings.TrimSpace(arguments.Query) == "" {
		return nil, fmt.Errorf("tools: find query is required")
	}
	if arguments.Limit < 0 {
		return nil, fmt.Errorf("tools: find limit cannot be negative")
	}
	return arguments, nil
}

func (tool FindTool) Execute(ctx context.Context, execution ExecutionContext, arguments any) (ToolResult, error) {
	args, ok := arguments.(findArguments)
	if !ok {
		return ToolResult{}, fmt.Errorf("tools: find arguments have type %T", arguments)
	}
	workspace, err := resolveToolWorkspace(tool.Workspace, execution.Session)
	if err != nil {
		return ToolResult{}, err
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
	terms := searchTerms(args.Query, args.GrepKeywords)
	if len(terms) == 0 {
		return ToolResult{}, fmt.Errorf("tools: find query has no searchable terms")
	}
	candidates := make([]findCandidate, 0)
	visit := func(absolute, relative string, entry fs.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(absolute)
		if err != nil || isBinary(data) {
			return nil
		}
		text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
		searchText := strings.ToLower(text)
		name := strings.ToLower(relative)
		score := 0
		for _, term := range terms {
			score += strings.Count(name, term) * 5
			score += strings.Count(searchText, term)
		}
		if score == 0 {
			return nil
		}
		lines := splitLines(text)
		line := 0
		for index, value := range lines {
			lowerValue := strings.ToLower(value)
			for _, term := range terms {
				if strings.Contains(lowerValue, term) {
					line = index + 1
					break
				}
			}
			if line > 0 {
				break
			}
		}
		if line == 0 {
			line = 1
		}
		snippet := ""
		if len(lines) >= line {
			snippet = strings.TrimSpace(lines[line-1])
		}
		candidates = append(candidates, findCandidate{Path: relative, Score: score, Line: line, Text: snippet})
		return nil
	}
	if info.IsDir() {
		if err := walkWorkspace(ctx, workspace, resolved.Absolute, walkOptions{}, visit); err != nil {
			return ToolResult{}, err
		}
	} else if err := visit(resolved.Absolute, resolved.Relative, fs.FileInfoToDirEntry(info)); err != nil {
		return ToolResult{}, err
	}
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].Score != candidates[right].Score {
			return candidates[left].Score > candidates[right].Score
		}
		return candidates[left].Path < candidates[right].Path
	})
	limit := args.Limit
	if limit <= 0 {
		limit = DefaultFindLimit
	}
	truncated := len(candidates) > limit
	if truncated {
		candidates = candidates[:limit]
	}
	lines := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		lines = append(lines, fmt.Sprintf("%s:%d  score=%d  %s", candidate.Path, candidate.Line, candidate.Score, candidate.Text))
	}
	limited, err := tool.Limiter.Apply(args.Query, []byte(strings.Join(lines, "\n")))
	if err != nil {
		return ToolResult{}, err
	}
	truncated = truncated || limited.Truncated
	details := map[string]any{
		"query":     args.Query,
		"matches":   candidates,
		"limit":     limit,
		"truncated": truncated,
	}
	if limited.Artifact != nil {
		details["artifact"] = *limited.Artifact
	}
	return ToolResult{Content: limited.Text, Details: details, Metadata: ResultMetadata{
		OutputBytes: limited.Bytes,
		Truncated:   truncated,
	}}, nil
}

func searchTerms(query string, keywords []string) []string {
	seen := make(map[string]struct{})
	terms := make([]string, 0)
	for _, raw := range append(strings.Fields(strings.ToLower(query)), keywords...) {
		term := strings.TrimSpace(strings.ToLower(raw))
		if len(term) < 2 {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	return terms
}
