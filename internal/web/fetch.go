package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// FetchRequest describes one web page fetch.
type FetchRequest struct {
	URL      string
	Raw      bool
	MaxChars int
}

// FetchResponse is the normalized page content returned to the tool.
type FetchResponse struct {
	URL         string
	FinalURL    string
	ContentType string
	Method      string
	Content     string
	Truncated   bool
	FetchedAt   time.Time
}

func (runtime *Runtime) Fetch(ctx context.Context, request FetchRequest) (FetchResponse, error) {
	if !runtime.FetchConfig.IsEnabled() {
		return FetchResponse{}, fmt.Errorf("web: fetch is disabled")
	}
	target := strings.TrimSpace(request.URL)
	if !strings.Contains(target, "://") {
		target = "https://" + target
	}
	parsed, err := validateURL(target)
	if err != nil {
		return FetchResponse{}, err
	}
	maxBytes := runtime.FetchConfig.MaxBodyBytes()
	fetchContext, cancel := withTimeout(ctx, time.Duration(runtime.FetchConfig.TimeoutSeconds())*time.Second)
	defer cancel()
	body, contentType, finalURL, err := runtime.fetchPage(fetchContext, parsed.String(), maxBytes)
	if err != nil {
		if fallback, fallbackErr := runtime.fetchRemoteReader(fetchContext, parsed.String(), request.MaxChars); fallbackErr == nil {
			return fallback, nil
		}
		return FetchResponse{}, err
	}
	if request.Raw {
		content, truncated := limitText(string(body), request.MaxChars)
		return FetchResponse{URL: parsed.String(), FinalURL: finalURL, ContentType: contentType, Method: "raw", Content: content, Truncated: truncated, FetchedAt: time.Now().UTC()}, nil
	}
	content, method, err := renderFetchedBody(body, contentType)
	if err != nil {
		if fallback, fallbackErr := runtime.fetchRemoteReader(fetchContext, parsed.String(), request.MaxChars); fallbackErr == nil {
			return fallback, nil
		}
		return FetchResponse{}, err
	}
	if len(strings.TrimSpace(content)) < 80 && (strings.Contains(strings.ToLower(contentType), "html") || challengePage(string(body))) {
		if fallback, fallbackErr := runtime.fetchRemoteReader(fetchContext, parsed.String(), request.MaxChars); fallbackErr == nil {
			return fallback, nil
		}
	}
	content, truncated := limitText(content, request.MaxChars)
	return FetchResponse{URL: parsed.String(), FinalURL: finalURL, ContentType: contentType, Method: method, Content: content, Truncated: truncated, FetchedAt: time.Now().UTC()}, nil
}

func (runtime *Runtime) fetchPage(ctx context.Context, target string, maxBytes int64) ([]byte, string, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", "", err
	}
	request.Header.Set("User-Agent", runtime.UserAgent)
	request.Header.Set("Accept", "text/markdown, text/plain;q=0.9, text/html;q=0.8, application/json;q=0.5")
	response, err := runtime.HTTP.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", "", ctx.Err()
		}
		return nil, "", "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, defaultMaxErrorBytes))
		return nil, "", "", providerError("fetch", response.StatusCode, strings.TrimSpace(string(message)), nil)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, "", "", err
	}
	if int64(len(body)) > maxBytes {
		return nil, "", "", fmt.Errorf("web: response exceeds %d bytes", maxBytes)
	}
	finalURL := target
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	return body, response.Header.Get("Content-Type"), finalURL, nil
}

func renderFetchedBody(body []byte, contentType string) (string, string, error) {
	mime := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	text := string(body)
	if mime == "application/json" || strings.HasSuffix(mime, "+json") {
		var value any
		if err := json.Unmarshal(body, &value); err != nil {
			return "", "", fmt.Errorf("web: decode JSON page: %w", err)
		}
		pretty, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return "", "", err
		}
		return string(pretty), "json", nil
	}
	if strings.Contains(mime, "html") || looksLikeHTML(text) {
		content, err := renderHTMLText(text)
		return content, "html", err
	}
	if strings.Contains(mime, "xml") || strings.Contains(mime, "rss") || strings.Contains(mime, "atom") {
		return text, "xml", nil
	}
	return strings.TrimSpace(text), "text", nil
}

func looksLikeHTML(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(lower, "<!doctype html") || strings.Contains(lower, "<html") || strings.Contains(lower, "<body")
}

func (runtime *Runtime) fetchRemoteReader(ctx context.Context, target string, maxChars int) (FetchResponse, error) {
	if key := runtime.bearer("firecrawl"); key != "" {
		data, _, err := runtime.postJSON(ctx, "firecrawl", providerEndpoint(runtime, "firecrawl", "https://api.firecrawl.dev/v2", "/scrape"), http.Header{"Authorization": []string{"Bearer " + key}, "Accept": []string{"application/json"}}, map[string]any{"url": target, "formats": []string{"markdown"}}, defaultMaxResponseBytes)
		if err == nil {
			var payload struct {
				Success bool `json:"success"`
				Data    struct {
					Markdown string `json:"markdown"`
				} `json:"data"`
			}
			if json.Unmarshal(data, &payload) == nil && strings.TrimSpace(payload.Data.Markdown) != "" {
				content, truncated := limitText(payload.Data.Markdown, maxChars)
				return FetchResponse{URL: target, FinalURL: target, ContentType: "text/markdown", Method: "firecrawl", Content: content, Truncated: truncated, FetchedAt: time.Now().UTC()}, nil
			}
		}
	}
	if key := runtime.bearer("jina"); key != "" {
		readerURL := "https://r.jina.ai/" + target
		headers := http.Header{"Authorization": []string{"Bearer " + key}, "Accept": []string{"text/markdown"}}
		data, _, err := runtime.do(ctx, "jina", http.MethodGet, readerURL, headers, nil, defaultMaxResponseBytes)
		if err == nil && strings.TrimSpace(string(data)) != "" {
			content, truncated := limitText(string(data), maxChars)
			return FetchResponse{URL: target, FinalURL: target, ContentType: "text/markdown", Method: "jina", Content: content, Truncated: truncated, FetchedAt: time.Now().UTC()}, nil
		}
	}
	return FetchResponse{}, fmt.Errorf("web: no remote reader returned usable content")
}

func limitText(text string, maxChars int) (string, bool) {
	text = strings.TrimSpace(text)
	if maxChars <= 0 || len([]rune(text)) <= maxChars {
		return text, false
	}
	runes := []rune(text)
	return string(runes[:maxChars]), true
}
