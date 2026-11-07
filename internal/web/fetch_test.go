package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentsd/internal/config"
)

func TestRuntimeFetchRendersHTMLAndTracksRedirect(t *testing.T) {
	finalServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write([]byte(`<html><head><title>Ignored</title><script>alert(1)</script></head><body><h1>Heading</h1><p>Hello <strong>world</strong>.</p><a href="https://example.com">Link</a></body></html>`))
	}))
	defer finalServer.Close()
	redirectServer := httptest.NewServer(http.RedirectHandler(finalServer.URL, http.StatusFound))
	defer redirectServer.Close()

	runtime := NewRuntime(config.SearchConfig{}, config.FetchConfig{}, nil, ModelContext{}, redirectServer.Client())
	response, err := runtime.Fetch(context.Background(), FetchRequest{URL: redirectServer.URL})
	if err != nil {
		t.Fatal(err)
	}
	if response.Method != "html" || response.FinalURL != finalServer.URL || !strings.Contains(response.Content, "Heading") || !strings.Contains(response.Content, "Hello world.") {
		t.Fatalf("response = %#v", response)
	}
	if strings.Contains(response.Content, "alert(1)") {
		t.Fatalf("script content leaked into response: %q", response.Content)
	}
}

func TestRuntimeFetchPrettyPrintsJSONAndHonorsMaxChars(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"answer":"abcdefghijklmnopqrstuvwxyz","items":[1,2,3]}`))
	}))
	defer server.Close()

	runtime := NewRuntime(config.SearchConfig{}, config.FetchConfig{}, nil, ModelContext{}, server.Client())
	response, err := runtime.Fetch(context.Background(), FetchRequest{URL: server.URL, MaxChars: 18})
	if err != nil {
		t.Fatal(err)
	}
	if response.Method != "json" || !response.Truncated || len([]rune(response.Content)) != 18 {
		t.Fatalf("response = %#v", response)
	}
	if !strings.Contains(response.Content, "\n") {
		t.Fatalf("JSON was not formatted: %q", response.Content)
	}
}

func TestRuntimeFetchRawReturnsOriginalBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte("raw body"))
	}))
	defer server.Close()

	runtime := NewRuntime(config.SearchConfig{}, config.FetchConfig{}, nil, ModelContext{}, server.Client())
	response, err := runtime.Fetch(context.Background(), FetchRequest{URL: server.URL, Raw: true})
	if err != nil {
		t.Fatal(err)
	}
	if response.Method != "raw" || response.Content != "raw body" {
		t.Fatalf("response = %#v", response)
	}
}
