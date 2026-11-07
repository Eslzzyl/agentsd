package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentsd/internal/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestSearchClientUsesConfiguredProviderAndAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Query().Get("q") != "golang" {
			t.Errorf("request = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("X-Subscription-Token") != "brave-secret" {
			t.Errorf("subscription token = %q", request.Header.Get("X-Subscription-Token"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"web":{"results":[{"title":"Go","url":"https://go.dev","description":"The Go language"}]}}`))
	}))
	defer server.Close()

	runtime := NewRuntime(
		config.SearchConfig{Providers: map[string]config.SearchProviderConfig{
			"brave": {Endpoint: server.URL + "/search"},
		}},
		config.FetchConfig{},
		config.AuthStore{"brave": {APIKey: "brave-secret"}},
		ModelContext{},
		server.Client(),
	)
	response, err := NewSearchClient(runtime).Search(context.Background(), SearchRequest{Provider: "brave", Query: "golang"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != "brave" || len(response.Sources) != 1 || response.Sources[0].URL != "https://go.dev" {
		t.Fatalf("response = %#v", response)
	}
}

func TestSearchClientFallsBackAfterChallenge(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case strings.Contains(request.URL.Host, "duckduckgo"):
			return jsonResponse(http.StatusOK, `<html><body>captcha verify you are human</body></html>`), nil
		case strings.Contains(request.URL.Host, "google.com"):
			return jsonResponse(http.StatusOK, `<html><body><a href="/url?q=https%3A%2F%2Fexample.com"><h3>Example</h3></a></body></html>`), nil
		default:
			return jsonResponse(http.StatusNotFound, "not found"), nil
		}
	})}
	runtime := NewRuntime(config.SearchConfig{Order: []string{"duckduckgo", "google"}}, config.FetchConfig{}, nil, ModelContext{}, client)
	response, err := NewSearchClient(runtime).Search(context.Background(), SearchRequest{Query: "fallback"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != "google" || len(response.Sources) != 1 || response.Sources[0].Title != "Example" {
		t.Fatalf("response = %#v", response)
	}
}

func TestSearchClientExposesAllReferenceProviders(t *testing.T) {
	want := map[string]bool{
		"duckduckgo": true, "google": true, "ecosia": true, "startpage": true, "mojeek": true,
		"parallel": true, "exa": true, "firecrawl": true, "perplexity": true, "public": true, "searxng": true,
		"zai": true, "tinyfish": true, "jina": true, "kagi": true, "tavily": true, "brave": true,
		"deepseek": true, "kimi": true, "synthetic": true, "ollama": true, "gemini": true,
		"anthropic": true, "codex": true, "xai": true, "openrouter": true, "openai": true,
	}
	got := NewSearchClient(nil).ProviderIDs()
	if len(got) != len(want) {
		t.Fatalf("provider count = %d, want %d (%v)", len(got), len(want), got)
	}
	for _, provider := range got {
		if !want[provider] {
			t.Fatalf("unexpected provider %q", provider)
		}
	}
}

func TestSearchClientHandlesMCPStructuredContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"structuredContent":{"results":[{"title":"Example","url":"https://example.com","excerpts":["snippet"]}]}}}`))
	}))
	defer server.Close()

	runtime := NewRuntime(config.SearchConfig{Providers: map[string]config.SearchProviderConfig{
		"exa": {Endpoint: server.URL},
	}}, config.FetchConfig{}, nil, ModelContext{}, server.Client())
	response, err := NewSearchClient(runtime).Search(context.Background(), SearchRequest{Provider: "exa", Query: "example"})
	if err != nil {
		t.Fatal(err)
	}
	if response.Provider != "exa" || len(response.Sources) != 1 || response.Sources[0].Snippet != "snippet" {
		t.Fatalf("response = %#v", response)
	}
}

func TestParseSSESearchResponseCollectsAnswerAndSources(t *testing.T) {
	response, err := parseSSESearchResponse("perplexity", []byte("data: {\"text\":\"answer\"}\ndata: {\"sources_list\":[{\"title\":\"Source\",\"url\":\"https://example.com\",\"snippet\":\"snippet\"}]}\n"), 5, "anonymous")
	if err != nil {
		t.Fatal(err)
	}
	if response.Answer != "answer" || len(response.Sources) != 1 || response.Sources[0].Title != "Source" {
		t.Fatalf("response = %#v", response)
	}
}
