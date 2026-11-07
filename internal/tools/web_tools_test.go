package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentsd/internal/config"
	"agentsd/internal/web"
)

func TestWebToolsRegisterAndDecodeArguments(t *testing.T) {
	runtime := web.NewRuntime(config.SearchConfig{}, config.FetchConfig{}, nil, web.ModelContext{}, nil)
	registry, err := NewRegistry(
		WebSearchTool{Client: web.NewSearchClient(runtime)},
		WebFetchTool{Runtime: runtime},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !registry.Visible("web_search") || !registry.Visible("web_fetch") {
		t.Fatal("web tools are not visible")
	}
	searchSpec, ok := registry.Spec("web_search")
	if !ok {
		t.Fatal("web_search spec is missing")
	}
	if _, err := searchSpec.Decode([]byte(`{"query":"golang","provider":"duckduckgo"}`)); err != nil {
		t.Fatal(err)
	}
	fetchSpec, ok := registry.Spec("web_fetch")
	if !ok {
		t.Fatal("web_fetch spec is missing")
	}
	if _, err := fetchSpec.Decode([]byte(`{"url":"https://example.com","max_chars":100}`)); err != nil {
		t.Fatal(err)
	}
}

func TestWebFetchToolReturnsRenderedPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<main><h1>Title</h1><p>Body</p></main>`))
	}))
	defer server.Close()

	runtime := web.NewRuntime(config.SearchConfig{}, config.FetchConfig{}, nil, web.ModelContext{}, server.Client())
	tool := WebFetchTool{Runtime: runtime}
	arguments, err := tool.Spec().Decode([]byte(`{"url":"` + server.URL + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), ExecutionContext{}, arguments)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "Title") || !strings.Contains(result.Content, "Body") {
		t.Fatalf("result = %#v", result)
	}
}
