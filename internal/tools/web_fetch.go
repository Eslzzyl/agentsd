package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentsd/internal/web"
)

// WebFetchTool retrieves a URL and renders it into model-readable text.
type WebFetchTool struct {
	Runtime *web.Runtime
	Limiter OutputLimiter
}

type webFetchArguments struct {
	URL      string `json:"url"`
	Raw      bool   `json:"raw,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}

func (WebFetchTool) Spec() ToolSpec {
	return ToolSpec{
		Name:        "web_fetch",
		Description: "Fetch a web page and return readable text or raw response content.",
		Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"url":{"type":"string"},"raw":{"type":"boolean"},"max_chars":{"type":"integer","minimum":1}},"required":["url"]}`),
		Policy:      ToolPolicy{Tier: TierRead, Timeout: 30 * time.Second, Concurrency: ConcurrencyShared, Interruptible: true},
		Decode:      decodeWebFetchArguments,
	}
}

func decodeWebFetchArguments(raw json.RawMessage) (any, error) {
	var arguments webFetchArguments
	if err := decodeJSON(raw, &arguments); err != nil {
		return nil, fmt.Errorf("tools: decode web_fetch arguments: %w", err)
	}
	if strings.TrimSpace(arguments.URL) == "" {
		return nil, fmt.Errorf("tools: web_fetch URL is required")
	}
	if arguments.MaxChars < 0 {
		return nil, fmt.Errorf("tools: web_fetch max_chars cannot be negative")
	}
	return arguments, nil
}

func (tool WebFetchTool) Execute(ctx context.Context, execution ExecutionContext, arguments any) (ToolResult, error) {
	parsed, ok := arguments.(webFetchArguments)
	if !ok {
		return ToolResult{}, fmt.Errorf("tools: web_fetch arguments have type %T", arguments)
	}
	if tool.Runtime == nil {
		return ToolResult{}, fmt.Errorf("tools: web_fetch runtime is unavailable")
	}
	response, err := tool.Runtime.Fetch(ctx, web.FetchRequest{URL: parsed.URL, Raw: parsed.Raw, MaxChars: parsed.MaxChars})
	if err != nil {
		return ToolResult{}, err
	}
	limited, err := tool.Limiter.Apply("web_fetch", []byte(response.Content))
	if err != nil {
		return ToolResult{}, err
	}
	details := map[string]any{
		"url":          response.URL,
		"final_url":    response.FinalURL,
		"content_type": response.ContentType,
		"method":       response.Method,
		"fetched_at":   response.FetchedAt,
		"bytes":        limited.Bytes,
		"truncated":    response.Truncated || limited.Truncated,
	}
	if limited.Artifact != nil {
		details["artifact"] = *limited.Artifact
	}
	return ToolResult{Content: limited.Text, Details: details, Metadata: ResultMetadata{OutputBytes: limited.Bytes, Truncated: response.Truncated || limited.Truncated}}, nil
}
