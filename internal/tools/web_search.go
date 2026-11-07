package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentsd/internal/web"
)

// WebSearchTool exposes the provider-neutral web search API to the model.
type WebSearchTool struct {
	Client  *web.SearchClient
	Limiter OutputLimiter
}

type webSearchArguments struct {
	Query           string  `json:"query"`
	Provider        string  `json:"provider,omitempty"`
	Recency         string  `json:"recency,omitempty"`
	Limit           int     `json:"limit,omitempty"`
	NumSearchResult int     `json:"num_search_results,omitempty"`
	MaxTokens       int     `json:"max_tokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
}

func (WebSearchTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "web_search",
		Description: "Search the public web and configured search services. Use provider only when a specific service is required.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string"},"provider":{"type":"string"},"recency":{"type":"string","enum":["day","week","month","year"]},"limit":{"type":"integer","minimum":1},"num_search_results":{"type":"integer","minimum":1},"max_tokens":{"type":"integer","minimum":1},"temperature":{"type":"number","minimum":0,"maximum":2}},"required":["query"]}`),
		Policy:      ToolPolicy{Tier: TierRead, Timeout: 60 * time.Second, Concurrency: ConcurrencyShared, Interruptible: true},
		Decode:      decodeWebSearchArguments,
	}
}

func decodeWebSearchArguments(raw json.RawMessage) (any, error) {
	var arguments webSearchArguments
	if err := decodeJSON(raw, &arguments); err != nil {
		return nil, fmt.Errorf("tools: decode web_search arguments: %w", err)
	}
	if strings.TrimSpace(arguments.Query) == "" {
		return nil, fmt.Errorf("tools: web_search query is required")
	}
	if arguments.Limit < 0 || arguments.NumSearchResult < 0 || arguments.MaxTokens < 0 {
		return nil, fmt.Errorf("tools: web_search numeric limits cannot be negative")
	}
	if arguments.Temperature < 0 || arguments.Temperature > 2 {
		return nil, fmt.Errorf("tools: web_search temperature must be between 0 and 2")
	}
	return arguments, nil
}

func (tool WebSearchTool) Execute(ctx context.Context, execution ExecutionContext, arguments any) (ToolResult, error) {
	parsed, ok := arguments.(webSearchArguments)
	if !ok {
		return ToolResult{}, fmt.Errorf("tools: web_search arguments have type %T", arguments)
	}
	if tool.Client == nil {
		return ToolResult{}, fmt.Errorf("tools: web_search client is unavailable")
	}
	request := web.SearchRequest{
		Query:           parsed.Query,
		Provider:        parsed.Provider,
		Recency:         parsed.Recency,
		Limit:           parsed.Limit,
		NumSearchResult: parsed.NumSearchResult,
		MaxTokens:       parsed.MaxTokens,
	}
	if parsed.Temperature != 0 {
		request.Temperature = &parsed.Temperature
	}
	response, err := tool.Client.Search(ctx, request)
	if err != nil {
		return ToolResult{}, err
	}
	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return ToolResult{}, fmt.Errorf("tools: encode web_search result: %w", err)
	}
	limited, err := tool.Limiter.Apply("web_search", data)
	if err != nil {
		return ToolResult{}, err
	}
	details := map[string]any{
		"provider":  response.Provider,
		"sources":   len(response.Sources),
		"bytes":     limited.Bytes,
		"truncated": limited.Truncated,
	}
	if limited.Artifact != nil {
		details["artifact"] = *limited.Artifact
	}
	return ToolResult{Content: limited.Text, Details: details, Metadata: ResultMetadata{OutputBytes: limited.Bytes, Truncated: limited.Truncated}}, nil
}
