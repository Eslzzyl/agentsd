package config

import (
	"testing"
)

func TestSearchAndFetchDefaultsAreEnabledWithoutSections(t *testing.T) {
	search := SearchConfig{}
	fetch := FetchConfig{}
	if !search.IsEnabled() || !fetch.IsEnabled() {
		t.Fatal("default network tools are disabled")
	}
	if search.TimeoutSeconds() != DefaultSearchTimeoutSeconds || search.MaxResults() != DefaultSearchMaxResults {
		t.Fatalf("search defaults = timeout %d, results %d", search.TimeoutSeconds(), search.MaxResults())
	}
	if fetch.TimeoutSeconds() != DefaultFetchTimeoutSeconds || fetch.MaxBodyBytes() != DefaultFetchMaxBytes {
		t.Fatalf("fetch defaults = timeout %d, bytes %d", fetch.TimeoutSeconds(), fetch.MaxBodyBytes())
	}
	order := search.ProviderOrder()
	if len(order) == 0 || order[0] != "duckduckgo" {
		t.Fatalf("default search order = %#v", order)
	}
}

func TestSearchProviderOptionsAndAuthReferenceParse(t *testing.T) {
	cfg, err := ParseTOML([]byte(`
provider = "gateway"
model = "model"

[providers.gateway]
api_type = "openai"

[providers.gateway.models.model]
context_window = 4096

[search]
max_results = 6
order = ["brave"]

[search.providers.brave]
endpoint = "https://search.example.test"
auth_ref = "custom-brave"
model = "search-model"

[search.providers.searxng.options]
engines = "google,bing"

[fetch]
timeout_seconds = 12
max_bytes = 100000
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Search.MaxResults() != 6 || len(cfg.Search.ProviderOrder()) != 1 || cfg.Search.ProviderOrder()[0] != "brave" {
		t.Fatalf("search config = %#v", cfg.Search)
	}
	brave := cfg.Search.Provider("brave")
	if brave.Endpoint != "https://search.example.test" || brave.AuthRef != "custom-brave" || brave.Model != "search-model" {
		t.Fatalf("brave config = %#v", brave)
	}
	if cfg.Search.Provider("searxng").Options["engines"] != "google,bing" {
		t.Fatalf("searxng options = %#v", cfg.Search.Provider("searxng").Options)
	}
	if cfg.Fetch.TimeoutSeconds() != 12 || cfg.Fetch.MaxBodyBytes() != 100000 {
		t.Fatalf("fetch config = %#v", cfg.Fetch)
	}
}
