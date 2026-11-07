package web

import (
	"fmt"
	"net/http"
	"strings"

	"agentsd/internal/config"
)

// SearchRequest is the provider-neutral web search request.
type SearchRequest struct {
	Query           string
	Provider        string
	Recency         string
	Limit           int
	NumSearchResult int
	MaxTokens       int
	Temperature     *float64
}

// SearchSource is one cited web document.
type SearchSource struct {
	Title         string `json:"title"`
	URL           string `json:"url"`
	Snippet       string `json:"snippet,omitempty"`
	PublishedDate string `json:"published_date,omitempty"`
	Author        string `json:"author,omitempty"`
}

// SearchCitation carries a provider's text citation when available.
type SearchCitation struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	CitedText string `json:"cited_text,omitempty"`
}

// SearchUsage records provider-side token or request accounting.
type SearchUsage struct {
	InputTokens    int `json:"input_tokens,omitempty"`
	OutputTokens   int `json:"output_tokens,omitempty"`
	SearchRequests int `json:"search_requests,omitempty"`
	TotalTokens    int `json:"total_tokens,omitempty"`
}

// SearchResponse is the stable model-facing search result.
type SearchResponse struct {
	Provider         string           `json:"provider"`
	Answer           string           `json:"answer,omitempty"`
	Sources          []SearchSource   `json:"sources"`
	Citations        []SearchCitation `json:"citations,omitempty"`
	RelatedQuestions []string         `json:"related_questions,omitempty"`
	Usage            *SearchUsage     `json:"usage,omitempty"`
	Model            string           `json:"model,omitempty"`
	RequestID        string           `json:"request_id,omitempty"`
	AuthMode         string           `json:"auth_mode,omitempty"`
}

// ModelContext gives grounded providers the selected model transport.
type ModelContext struct {
	ProviderID     string
	ModelID        string
	RequestModelID string
	APIType        string
	BaseURL        string
	ExtraBody      map[string]any
}

// Runtime owns shared network state for search and fetching.
type Runtime struct {
	SearchConfig config.SearchConfig
	FetchConfig  config.FetchConfig
	Auth         config.AuthStore
	Model        ModelContext
	HTTP         *http.Client
	UserAgent    string
}

func NewRuntime(searchConfig config.SearchConfig, fetchConfig config.FetchConfig, auth config.AuthStore, model ModelContext, client *http.Client) *Runtime {
	if client == nil {
		client = &http.Client{}
	}
	return &Runtime{
		SearchConfig: searchConfig,
		FetchConfig:  fetchConfig,
		Auth:         auth,
		Model:        model,
		HTTP:         client,
		UserAgent:    "agentsd/0.1 (+https://github.com/agentsd)",
	}
}

func (runtime *Runtime) providerConfig(id string) config.SearchProviderConfig {
	return runtime.SearchConfig.Provider(id)
}

func (runtime *Runtime) authEntry(id string) config.ProviderAuth {
	provider := runtime.providerConfig(id)
	ref := strings.TrimSpace(provider.AuthRef)
	if ref == "" {
		ref = defaultAuthRef(id)
	}
	return runtime.Auth.Provider(ref)
}

func (runtime *Runtime) bearer(id string) string {
	provider := runtime.providerConfig(id)
	ref := strings.TrimSpace(provider.AuthRef)
	if ref == "" {
		ref = defaultAuthRef(id)
	}
	return runtime.Auth.Bearer(ref)
}

func defaultAuthRef(provider string) string {
	switch provider {
	case "kimi":
		return "kimi-code"
	case "ollama":
		return "ollama-cloud"
	case "gemini":
		return "google"
	case "codex":
		return "openai-codex"
	default:
		return provider
	}
}

func (runtime *Runtime) endpoint(provider, fallback string) string {
	if configured := strings.TrimRight(strings.TrimSpace(runtime.providerConfig(provider).Endpoint), "/"); configured != "" {
		return configured
	}
	return strings.TrimRight(fallback, "/")
}

// ProviderError preserves provider identity and HTTP status for fallback decisions.
type ProviderError struct {
	Provider string
	Status   int
	Message  string
	Err      error
}

func (err *ProviderError) Error() string {
	if err == nil {
		return ""
	}
	if err.Message != "" {
		if err.Status > 0 {
			return fmt.Sprintf("%s: %s (HTTP %d)", err.Provider, err.Message, err.Status)
		}
		return fmt.Sprintf("%s: %s", err.Provider, err.Message)
	}
	if err.Err != nil {
		return fmt.Sprintf("%s: %v", err.Provider, err.Err)
	}
	return err.Provider + ": request failed"
}

func (err *ProviderError) Unwrap() error { return err.Err }

func providerError(provider string, status int, message string, cause error) error {
	return &ProviderError{Provider: provider, Status: status, Message: message, Err: cause}
}

func responseHasContent(response SearchResponse) bool {
	return strings.TrimSpace(response.Answer) != "" || len(response.Sources) > 0 || len(response.Citations) > 0
}

func validateSearchRequest(request SearchRequest, maxResults int) (SearchRequest, error) {
	request.Query = strings.TrimSpace(request.Query)
	request.Provider = strings.TrimSpace(strings.ToLower(request.Provider))
	request.Recency = strings.TrimSpace(strings.ToLower(request.Recency))
	if request.Query == "" {
		return SearchRequest{}, fmt.Errorf("web: search query is required")
	}
	if request.Recency != "" && request.Recency != "day" && request.Recency != "week" && request.Recency != "month" && request.Recency != "year" {
		return SearchRequest{}, fmt.Errorf("web: unsupported recency %q", request.Recency)
	}
	if maxResults <= 0 {
		maxResults = config.DefaultSearchMaxResults
	}
	if request.Limit <= 0 {
		request.Limit = maxResults
	}
	if request.NumSearchResult <= 0 {
		request.NumSearchResult = request.Limit
	}
	if request.Limit > maxResults {
		request.Limit = maxResults
	}
	if request.NumSearchResult > maxResults {
		request.NumSearchResult = maxResults
	}
	if request.Limit < 1 || request.NumSearchResult < 1 {
		return SearchRequest{}, fmt.Errorf("web: search result limit must be positive")
	}
	return request, nil
}
