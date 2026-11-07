package web

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"agentsd/internal/config"
)

type searchProvider struct {
	id            string
	autoAvailable func(*Runtime) bool
	explicit      bool
	search        func(context.Context, *Runtime, SearchRequest) (SearchResponse, error)
}

type providerGate struct {
	mu    sync.Mutex
	next  time.Time
	delay time.Duration
}

// SearchClient coordinates all built-in providers and their fallback order.
type SearchClient struct {
	runtime   *Runtime
	providers map[string]searchProvider
	gates     map[string]*providerGate
}

func NewSearchClient(runtime *Runtime) *SearchClient {
	if runtime == nil {
		runtime = NewRuntime(configSearchDefaults(), configFetchDefaults(), nil, ModelContext{}, nil)
	}
	client := &SearchClient{
		runtime:   runtime,
		providers: makeProviderRegistry(),
		gates:     make(map[string]*providerGate),
	}
	for id := range client.providers {
		if id == "exa" {
			client.gates[id] = &providerGate{delay: time.Second}
		}
	}
	return client
}

func configSearchDefaults() config.SearchConfig { return config.SearchConfig{} }
func configFetchDefaults() config.FetchConfig   { return config.FetchConfig{} }

func (client *SearchClient) ProviderIDs() []string {
	ids := make([]string, 0, len(client.providers))
	for id := range client.providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (client *SearchClient) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	if !client.runtime.SearchConfig.IsEnabled() {
		return SearchResponse{}, fmt.Errorf("web: search is disabled")
	}
	request, err := validateSearchRequest(request, client.runtime.SearchConfig.MaxResults())
	if err != nil {
		return SearchResponse{}, err
	}
	searchContext, cancel := withTimeout(ctx, time.Duration(client.runtime.SearchConfig.TimeoutSeconds())*time.Second)
	defer cancel()
	if request.Provider != "" {
		provider, ok := client.providers[request.Provider]
		if !ok {
			return SearchResponse{}, fmt.Errorf("web: unknown search provider %q", request.Provider)
		}
		if !client.runtime.SearchConfig.Provider(request.Provider).IsEnabled() {
			return SearchResponse{}, fmt.Errorf("web: search provider %q is disabled", request.Provider)
		}
		if !provider.explicit && provider.autoAvailable != nil && !provider.autoAvailable(client.runtime) {
			return SearchResponse{}, fmt.Errorf("web: search provider %q is not configured", request.Provider)
		}
		return client.call(searchContext, provider, request)
	}

	failures := make([]string, 0)
	for _, id := range client.runtime.SearchConfig.ProviderOrder() {
		provider, ok := client.providers[id]
		if !ok || !client.runtime.SearchConfig.Provider(id).IsEnabled() {
			continue
		}
		if provider.autoAvailable != nil && !provider.autoAvailable(client.runtime) {
			continue
		}
		response, callErr := client.call(searchContext, provider, request)
		if callErr == nil && responseHasContent(response) {
			return response, nil
		}
		if callErr != nil {
			failures = append(failures, callErr.Error())
			if searchContext.Err() != nil {
				return SearchResponse{}, searchContext.Err()
			}
		}
	}
	if len(failures) == 0 {
		return SearchResponse{}, fmt.Errorf("web: no search provider is available; configure a key or select a public provider")
	}
	return SearchResponse{}, fmt.Errorf("web: all search providers failed: %s", strings.Join(failures, "; "))
}

func (client *SearchClient) call(ctx context.Context, provider searchProvider, request SearchRequest) (SearchResponse, error) {
	if err := client.waitSlot(ctx, provider.id); err != nil {
		return SearchResponse{}, err
	}
	response, err := provider.search(ctx, client.runtime, request)
	if err != nil {
		return SearchResponse{}, err
	}
	response.Provider = provider.id
	response.Sources = uniqueSources(response.Sources, request.NumSearchResult)
	return response, nil
}

func (client *SearchClient) waitSlot(ctx context.Context, provider string) error {
	gate := client.gates[provider]
	if gate == nil || gate.delay <= 0 {
		return nil
	}
	for {
		gate.mu.Lock()
		wait := time.Until(gate.next)
		if wait <= 0 {
			gate.next = time.Now().Add(gate.delay)
			gate.mu.Unlock()
			return nil
		}
		gate.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}
