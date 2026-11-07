package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

func makeProviderRegistry() map[string]searchProvider {
	public := func(*Runtime) bool { return true }
	key := func(provider string) func(*Runtime) bool {
		return func(runtime *Runtime) bool { return runtime.bearer(provider) != "" }
	}
	searx := func(runtime *Runtime) bool { return runtime.providerConfig("searxng").Endpoint != "" }
	perplexity := func(runtime *Runtime) bool {
		entry := runtime.authEntry("perplexity")
		return runtime.bearer("perplexity") != "" || strings.TrimSpace(entry.Cookies) != ""
	}
	return map[string]searchProvider{
		"duckduckgo": {id: "duckduckgo", autoAvailable: public, explicit: true, search: searchDuckDuckGo},
		"parallel":   {id: "parallel", autoAvailable: public, explicit: true, search: searchParallel},
		"exa":        {id: "exa", autoAvailable: public, explicit: true, search: searchExa},
		"firecrawl":  {id: "firecrawl", autoAvailable: public, explicit: true, search: searchFirecrawl},
		"google":     {id: "google", autoAvailable: public, explicit: true, search: searchGoogle},
		"ecosia":     {id: "ecosia", autoAvailable: public, explicit: true, search: searchEcosia},
		"startpage":  {id: "startpage", autoAvailable: public, explicit: true, search: searchStartpage},
		"mojeek":     {id: "mojeek", autoAvailable: public, explicit: true, search: searchMojeek},
		"public":     {id: "public", autoAvailable: func(*Runtime) bool { return false }, explicit: true, search: searchPublic},
		"perplexity": {id: "perplexity", autoAvailable: perplexity, explicit: true, search: searchPerplexity},
		"searxng":    {id: "searxng", autoAvailable: searx, explicit: true, search: searchSearXNG},
		"brave":      {id: "brave", autoAvailable: key("brave"), explicit: true, search: searchBrave},
		"tavily":     {id: "tavily", autoAvailable: key("tavily"), explicit: true, search: searchTavily},
		"jina":       {id: "jina", autoAvailable: key("jina"), explicit: true, search: searchJina},
		"kagi":       {id: "kagi", autoAvailable: key("kagi"), explicit: true, search: searchKagi},
		"tinyfish":   {id: "tinyfish", autoAvailable: key("tinyfish"), explicit: true, search: searchTinyFish},
		"zai":        {id: "zai", autoAvailable: key("zai"), explicit: true, search: searchZAI},
		"deepseek":   {id: "deepseek", autoAvailable: key("deepseek"), explicit: true, search: searchDeepSeek},
		"kimi":       {id: "kimi", autoAvailable: key("kimi"), explicit: true, search: searchKimi},
		"synthetic":  {id: "synthetic", autoAvailable: key("synthetic"), explicit: true, search: searchSynthetic},
		"ollama":     {id: "ollama", autoAvailable: key("ollama"), explicit: true, search: searchOllama},
		"gemini":     {id: "gemini", autoAvailable: key("gemini"), explicit: true, search: searchGemini},
		"anthropic":  {id: "anthropic", autoAvailable: key("anthropic"), explicit: true, search: searchAnthropic},
		"codex":      {id: "codex", autoAvailable: key("codex"), explicit: true, search: searchCodex},
		"xai":        {id: "xai", autoAvailable: key("xai"), explicit: true, search: searchXAI},
		"openrouter": {id: "openrouter", autoAvailable: key("openrouter"), explicit: true, search: searchOpenRouter},
		"openai":     {id: "openai", autoAvailable: key("openai"), explicit: true, search: searchOpenAI},
	}
}

func searchHTMLProvider(ctx context.Context, runtime *Runtime, request SearchRequest, provider, target string, method string, form url.Values) (SearchResponse, error) {
	var body []byte
	var headers = make(http.Header)
	headers.Set("Accept", "text/html,application/xhtml+xml")
	headers.Set("Accept-Language", "en-US,en;q=0.8")
	if method == http.MethodPost {
		headers.Set("Content-Type", "application/x-www-form-urlencoded")
		encoded := []byte(form.Encode())
		var err error
		body, _, err = runtime.do(ctx, provider, method, target, headers, encoded, defaultMaxResponseBytes)
		if err != nil {
			return SearchResponse{}, err
		}
	} else {
		var err error
		var text string
		text, _, err = runtime.getText(ctx, provider, target, headers, defaultMaxResponseBytes)
		if err != nil {
			return SearchResponse{}, err
		}
		body = []byte(text)
	}
	if challengePage(string(body)) {
		return SearchResponse{}, providerError(provider, http.StatusTooManyRequests, "upstream returned a challenge page", nil)
	}
	links, err := extractHTMLLinks(string(body), target)
	if err != nil {
		return SearchResponse{}, providerError(provider, 502, "parse search HTML", err)
	}
	host, _ := url.Parse(target)
	sources := make([]SearchSource, 0, request.NumSearchResult)
	for _, link := range links {
		if link.Title == "" || isSearchHost(link.URL, host.Hostname()) {
			continue
		}
		sources = append(sources, SearchSource{Title: link.Title, URL: link.URL})
	}
	if len(sources) == 0 {
		return SearchResponse{}, providerError(provider, 502, "search page returned no usable results", nil)
	}
	return SearchResponse{Provider: provider, Sources: uniqueSources(sources, request.NumSearchResult), AuthMode: "keyless"}, nil
}

func isSearchHost(raw, sourceHost string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return true
	}
	host := strings.ToLower(parsed.Hostname())
	sourceHost = strings.ToLower(sourceHost)
	if host == "" || host == sourceHost || strings.HasSuffix(host, "."+sourceHost) {
		return true
	}
	for _, blocked := range []string{"google.com", "googleusercontent.com", "duckduckgo.com", "ecosia.org", "startpage.com", "mojeek.com", "mojeek.de"} {
		if host == blocked || strings.HasSuffix(host, "."+blocked) {
			return true
		}
	}
	return false
}

func searchDuckDuckGo(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	form := url.Values{"q": {request.Query}, "kl": {"us-en"}}
	if request.Recency != "" {
		form.Set("df", map[string]string{"day": "d", "week": "w", "month": "m", "year": "y"}[request.Recency])
	}
	return searchHTMLProvider(ctx, runtime, request, "duckduckgo", "https://html.duckduckgo.com/html/", http.MethodPost, form)
}

func searchGoogle(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	return searchPublicHTML(ctx, runtime, request, "google", "https://www.google.com/search")
}

func searchEcosia(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	return searchPublicHTML(ctx, runtime, request, "ecosia", "https://www.ecosia.org/search")
}

func searchStartpage(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	return searchPublicHTML(ctx, runtime, request, "startpage", "https://www.startpage.com/sp/search")
}

func searchMojeek(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	return searchPublicHTML(ctx, runtime, request, "mojeek", "https://www.mojeek.com/search")
}

func searchPublicHTML(ctx context.Context, runtime *Runtime, request SearchRequest, provider, endpoint string) (SearchResponse, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return SearchResponse{}, err
	}
	query := parsed.Query()
	query.Set("q", request.Query)
	query.Set("num", fmt.Sprintf("%d", request.NumSearchResult))
	if provider == "google" && request.Recency != "" {
		query.Set("tbs", map[string]string{"day": "qdr:d", "week": "qdr:w", "month": "qdr:m", "year": "qdr:y"}[request.Recency])
	}
	return searchHTMLProvider(ctx, runtime, request, provider, parsed.String(), http.MethodGet, nil)
}

func searchPublic(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	providers := []func(context.Context, *Runtime, SearchRequest) (SearchResponse, error){
		searchDuckDuckGo, searchGoogle, searchEcosia, searchStartpage, searchMojeek,
	}
	child, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	type result struct {
		response SearchResponse
		err      error
	}
	results := make(chan result, len(providers))
	var wait sync.WaitGroup
	for _, provider := range providers {
		wait.Add(1)
		go func(provider func(context.Context, *Runtime, SearchRequest) (SearchResponse, error)) {
			defer wait.Done()
			response, err := provider(child, runtime, request)
			results <- result{response: response, err: err}
		}(provider)
	}
	wait.Wait()
	close(results)
	var sources []SearchSource
	for result := range results {
		if result.err == nil {
			sources = append(sources, result.response.Sources...)
		}
	}
	sources = uniqueSources(sources, request.NumSearchResult)
	if len(sources) == 0 {
		return SearchResponse{}, providerError("public", 502, "all public search engines failed", nil)
	}
	return SearchResponse{Provider: "public", Sources: sources, AuthMode: "keyless"}, nil
}

func sourceFromMap(record map[string]any) (SearchSource, bool) {
	rawURL := stringValue(mapValue(record, "url", "uri", "link", "href", "source_url", "source_website_url"))
	if rawURL == "" {
		return SearchSource{}, false
	}
	parsed, err := validateURL(rawURL)
	if err != nil {
		return SearchSource{}, false
	}
	title := stringValue(mapValue(record, "title", "name", "headline", "caption"))
	snippet := stringValue(mapValue(record, "snippet", "description", "content", "text", "summary", "excerpt"))
	if snippet == "" {
		if excerpts, ok := record["excerpts"].([]any); ok {
			parts := make([]string, 0, len(excerpts))
			for _, excerpt := range excerpts {
				if text := stringValue(excerpt); text != "" {
					parts = append(parts, text)
				}
			}
			snippet = strings.Join(parts, "\n\n")
		}
	}
	published := stringValue(mapValue(record, "published_date", "publish_date", "publishedDate", "published", "date", "age"))
	author := stringValue(mapValue(record, "author", "site_name", "media"))
	return SearchSource{Title: title, URL: parsed.String(), Snippet: snippet, PublishedDate: published, Author: author}, true
}

func collectLabeledTextSources(text string, sources *[]SearchSource) {
	if !strings.Contains(text, "Title:") || !strings.Contains(text, "URL:") {
		return
	}
	var title, rawURL, snippet, author, published string
	var collectingSnippet bool
	flush := func() {
		if rawURL == "" {
			return
		}
		parsed, err := validateURL(rawURL)
		if err != nil {
			return
		}
		if title == "" {
			title = parsed.String()
		}
		*sources = append(*sources, SearchSource{Title: title, URL: parsed.String(), Snippet: strings.TrimSpace(snippet), Author: author, PublishedDate: published})
		title, rawURL, snippet, author, published = "", "", "", "", ""
		collectingSnippet = false
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Title:"):
			if rawURL != "" {
				flush()
			}
			title = strings.TrimSpace(strings.TrimPrefix(line, "Title:"))
			collectingSnippet = false
		case strings.HasPrefix(line, "URL:"):
			rawURL = strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
			collectingSnippet = false
		case strings.HasPrefix(line, "Author:"):
			author = strings.TrimSpace(strings.TrimPrefix(line, "Author:"))
			collectingSnippet = false
		case strings.HasPrefix(line, "Published Date:"):
			published = strings.TrimSpace(strings.TrimPrefix(line, "Published Date:"))
			collectingSnippet = false
		case strings.HasPrefix(line, "Text:"):
			snippet = strings.TrimSpace(strings.TrimPrefix(line, "Text:"))
			collectingSnippet = true
		case collectingSnippet && line != "":
			if snippet != "" {
				snippet += "\n"
			}
			snippet += line
		}
	}
	flush()
}

func collectJSONSources(value any, sources *[]SearchSource, depth int) {
	if depth > 8 || value == nil {
		return
	}
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			collectJSONSources(item, sources, depth+1)
		}
	case map[string]any:
		source, hasSource := sourceFromMap(value)
		if hasSource {
			*sources = append(*sources, source)
		} else if text := stringValue(value["text"]); text != "" {
			collectLabeledTextSources(text, sources)
		}
		keys := []string{"results", "search_results", "search", "news", "video", "infobox", "direct_answer", "adjacent_question", "related_search", "web", "web_results", "web_result_block", "sources_list", "data", "content", "output", "items", "citations", "annotations", "references", "sources", "groundingMetadata", "groundingChunks"}
		for _, key := range keys {
			if nested, ok := value[key]; ok {
				collectJSONSources(nested, sources, depth+1)
			}
		}
	}
}

func collectAnswer(value any, depth int) string {
	if depth > 6 || value == nil {
		return ""
	}
	if record, ok := value.(map[string]any); ok {
		for _, key := range []string{"answer", "output_text", "response", "summary", "text"} {
			if text := stringValue(record[key]); text != "" {
				return text
			}
		}
		for _, key := range []string{"output", "choices", "message", "content", "data", "result", "candidates", "parts"} {
			if nested, ok := record[key]; ok {
				if text := collectAnswer(nested, depth+1); text != "" {
					return text
				}
			}
		}
	}
	if items, ok := value.([]any); ok {
		for _, item := range items {
			if text := collectAnswer(item, depth+1); text != "" {
				return text
			}
		}
	}
	return ""
}

func collectCitations(value any, citations *[]SearchCitation, depth int) {
	if depth > 8 || value == nil {
		return
	}
	switch value := value.(type) {
	case []any:
		for _, item := range value {
			if rawURL := stringValue(item); rawURL != "" {
				if parsed, err := validateURL(rawURL); err == nil {
					*citations = append(*citations, SearchCitation{URL: parsed.String()})
					continue
				}
			}
			if record, ok := item.(map[string]any); ok {
				if source, ok := sourceFromMap(record); ok {
					*citations = append(*citations, SearchCitation{URL: source.URL, Title: source.Title, CitedText: source.Snippet})
				} else {
					collectCitations(record, citations, depth+1)
				}
			}
		}
	case map[string]any:
		for _, key := range []string{"citations", "references", "sources", "groundingChunks"} {
			if nested, ok := value[key]; ok {
				collectCitations(nested, citations, depth+1)
			}
		}
	}
}

func uniqueCitations(citations []SearchCitation, limit int) []SearchCitation {
	seen := make(map[string]struct{}, len(citations))
	result := make([]SearchCitation, 0, minInt(limit, len(citations)))
	for _, citation := range citations {
		if citation.URL == "" {
			continue
		}
		if _, exists := seen[citation.URL]; exists {
			continue
		}
		seen[citation.URL] = struct{}{}
		result = append(result, citation)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}

func genericJSONResponse(provider string, data []byte, limit int, authMode string) (SearchResponse, error) {
	var payload any
	if err := decodeJSON(data, &payload); err != nil {
		return SearchResponse{}, providerError(provider, 502, "invalid JSON response", err)
	}
	var sources []SearchSource
	collectJSONSources(payload, &sources, 0)
	var citations []SearchCitation
	collectCitations(payload, &citations, 0)
	response := SearchResponse{
		Provider:  provider,
		Sources:   uniqueSources(sources, limit),
		Citations: uniqueCitations(citations, limit),
		Answer:    collectAnswer(payload, 0),
		AuthMode:  authMode,
	}
	if record, ok := payload.(map[string]any); ok {
		response.RequestID = stringValue(mapValue(record, "request_id", "requestId", "id"))
		response.Model = stringValue(mapValue(record, "model", "model_id"))
	}
	if !responseHasContent(response) {
		return SearchResponse{}, providerError(provider, 502, "response contained no search result", nil)
	}
	return response, nil
}

func searchBrave(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("brave")
	if key == "" {
		return SearchResponse{}, providerError("brave", 401, "API key is not configured", nil)
	}
	target := runtime.endpoint("brave", "https://api.search.brave.com/res/v1/web/search")
	parsed, err := url.Parse(target)
	if err != nil {
		return SearchResponse{}, err
	}
	query := parsed.Query()
	query.Set("q", request.Query)
	query.Set("count", fmt.Sprintf("%d", request.NumSearchResult))
	query.Set("extra_snippets", "true")
	parsed.RawQuery = query.Encode()
	headers := http.Header{"X-Subscription-Token": []string{key}, "Accept": []string{"application/json"}}
	data, responseHeaders, err := runtime.do(ctx, "brave", http.MethodGet, parsed.String(), headers, nil, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	response, err := genericJSONResponse("brave", data, request.NumSearchResult, "api_key")
	if err != nil {
		return SearchResponse{}, err
	}
	response.RequestID = responseHeaders.Get("x-request-id")
	return response, nil
}

func searchTavily(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("tavily")
	if key == "" {
		return SearchResponse{}, providerError("tavily", 401, "API key is not configured", nil)
	}
	payload := map[string]any{"query": request.Query, "search_depth": "basic", "max_results": request.NumSearchResult, "include_answer": "advanced"}
	if request.Recency != "" {
		payload["time_range"] = request.Recency
	}
	headers := http.Header{"Authorization": []string{"Bearer " + key}}
	data, _, err := runtime.postJSON(ctx, "tavily", runtime.endpoint("tavily", "https://api.tavily.com/search"), headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("tavily", data, request.NumSearchResult, "api_key")
}

func searchJina(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("jina")
	if key == "" {
		return SearchResponse{}, providerError("jina", 401, "API key is not configured", nil)
	}
	base := runtime.endpoint("jina", "https://s.jina.ai")
	target := base + "/" + url.PathEscape(request.Query)
	parsed, err := url.Parse(target)
	if err != nil {
		return SearchResponse{}, err
	}
	query := parsed.Query()
	query.Set("count", fmt.Sprintf("%d", request.NumSearchResult))
	parsed.RawQuery = query.Encode()
	headers := http.Header{"Authorization": []string{"Bearer " + key}, "Accept": []string{"application/json"}, "X-Respond-With": []string{"no-content"}}
	data, _, err := runtime.do(ctx, "jina", http.MethodGet, parsed.String(), headers, nil, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("jina", data, request.NumSearchResult, "api_key")
}

func searchKagi(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("kagi")
	if key == "" {
		return SearchResponse{}, providerError("kagi", 401, "API key is not configured", nil)
	}
	payload := map[string]any{"query": request.Query, "workflow": "search", "limit": request.NumSearchResult}
	if request.Recency != "" {
		payload["recency"] = request.Recency
	}
	headers := http.Header{"Authorization": []string{"Bearer " + key}, "Accept": []string{"application/json"}}
	data, _, err := runtime.postJSON(ctx, "kagi", runtime.endpoint("kagi", "https://kagi.com/api/v1/search"), headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("kagi", data, request.NumSearchResult, "api_key")
}

func searchTinyFish(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("tinyfish")
	if key == "" {
		return SearchResponse{}, providerError("tinyfish", 401, "API key is not configured", nil)
	}
	parsed, err := url.Parse(runtime.endpoint("tinyfish", "https://api.search.tinyfish.ai"))
	if err != nil {
		return SearchResponse{}, err
	}
	query := parsed.Query()
	query.Set("query", request.Query)
	query.Set("num_results", fmt.Sprintf("%d", request.NumSearchResult))
	if request.Recency != "" {
		minutes := map[string]string{"day": "1440", "week": "10080", "month": "43200", "year": "525600"}
		query.Set("recency_minutes", minutes[request.Recency])
	}
	parsed.RawQuery = query.Encode()
	headers := http.Header{"X-API-Key": []string{key}, "Accept": []string{"application/json"}}
	data, _, err := runtime.do(ctx, "tinyfish", http.MethodGet, parsed.String(), headers, nil, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("tinyfish", data, request.NumSearchResult, "api_key")
}

func searchDeepSeek(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("deepseek")
	if key == "" {
		return SearchResponse{}, providerError("deepseek", 401, "API key is not configured", nil)
	}
	payload := map[string]any{"query": request.Query, "count": request.NumSearchResult}
	headers := http.Header{"Authorization": []string{"Bearer " + key}, "Accept": []string{"application/json"}}
	data, _, err := runtime.postJSON(ctx, "deepseek", runtime.endpoint("deepseek", "https://api.deepseek.com/v1/tools/search"), headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("deepseek", data, request.NumSearchResult, "api_key")
}

func searchKimi(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("kimi")
	if key == "" {
		return SearchResponse{}, providerError("kimi", 401, "API key is not configured", nil)
	}
	payload := map[string]any{"text_query": request.Query, "limit": request.NumSearchResult, "enable_page_crawling": true, "timeout_seconds": 30}
	headers := http.Header{"Authorization": []string{"Bearer " + key}, "Accept": []string{"application/json"}}
	data, _, err := runtime.postJSON(ctx, "kimi", runtime.endpoint("kimi", "https://api.kimi.com/coding/v1/search"), headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("kimi", data, request.NumSearchResult, "api_key")
}

func searchSynthetic(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("synthetic")
	if key == "" {
		return SearchResponse{}, providerError("synthetic", 401, "API key is not configured", nil)
	}
	data, _, err := runtime.postJSON(ctx, "synthetic", runtime.endpoint("synthetic", "https://api.synthetic.new/v2/search"), http.Header{"Authorization": []string{"Bearer " + key}}, map[string]any{"query": request.Query}, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("synthetic", data, request.NumSearchResult, "api_key")
}

func searchOllama(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("ollama")
	if key == "" {
		return SearchResponse{}, providerError("ollama", 401, "API key is not configured", nil)
	}
	data, _, err := runtime.postJSON(ctx, "ollama", runtime.endpoint("ollama", "https://ollama.com/api/web_search"), http.Header{"Authorization": []string{"Bearer " + key}}, map[string]any{"query": request.Query, "max_results": request.NumSearchResult}, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("ollama", data, request.NumSearchResult, "api_key")
}

func searchSearXNG(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	endpoint := runtime.endpoint("searxng", "")
	if endpoint == "" {
		return SearchResponse{}, providerError("searxng", 400, "endpoint is not configured", nil)
	}
	parsed, err := url.Parse(strings.TrimRight(endpoint, "/") + "/search")
	if err != nil {
		return SearchResponse{}, err
	}
	query := parsed.Query()
	query.Set("q", request.Query)
	query.Set("format", "json")
	query.Set("pageno", "1")
	if request.Recency != "" {
		query.Set("time_range", map[string]string{"day": "day", "week": "month", "month": "month", "year": "year"}[request.Recency])
	}
	providerConfig := runtime.providerConfig("searxng")
	if value := optionString(providerConfig.Options, "categories"); value != "" {
		query.Set("categories", value)
	}
	if value := optionString(providerConfig.Options, "engines"); value != "" {
		query.Set("engines", value)
	}
	if value := optionString(providerConfig.Options, "language"); value != "" {
		query.Set("language", value)
	}
	parsed.RawQuery = query.Encode()
	headers := http.Header{"Accept": []string{"application/json"}}
	entry := runtime.authEntry("searxng")
	switch {
	case entry.Username != "" || entry.Password != "":
		headers.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(entry.Username+":"+entry.Password)))
	case runtime.bearer("searxng") != "":
		headers.Set("Authorization", "Bearer "+runtime.bearer("searxng"))
	}
	data, _, err := runtime.do(ctx, "searxng", http.MethodGet, parsed.String(), headers, nil, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("searxng", data, request.NumSearchResult, authModeFor(runtime, "searxng"))
}

func optionString(options map[string]any, key string) string {
	if options == nil {
		return ""
	}
	return stringValue(options[key])
}

func authModeFor(runtime *Runtime, provider string) string {
	entry := runtime.authEntry(provider)
	if entry.APIKey != "" {
		return "api_key"
	}
	if entry.AccessToken != "" {
		return "oauth"
	}
	if entry.Cookies != "" {
		return "cookies"
	}
	return "keyless"
}

func mcpCall(ctx context.Context, runtime *Runtime, provider, target string, headers http.Header, method string, params map[string]any) (any, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      fmt.Sprintf("%s-%d", provider, time.Now().UnixNano()),
		"method":  method,
		"params":  params,
	}
	data, _, err := runtime.postJSON(ctx, provider, target, headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return nil, err
	}
	response, err := parseMCPResponse(data)
	if err != nil {
		return nil, providerError(provider, 502, "invalid MCP response", err)
	}
	if errorValue, ok := response["error"].(map[string]any); ok {
		message := stringValue(errorValue["message"])
		if message == "" {
			message = "MCP request failed"
		}
		return nil, providerError(provider, 502, message, nil)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		return response, nil
	}
	if structured := result["structuredContent"]; structured != nil {
		return structured, nil
	}
	if content, ok := result["content"].([]any); ok {
		for _, item := range content {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text := stringValue(record["text"])
			if text == "" {
				continue
			}
			var parsed any
			if json.Unmarshal([]byte(text), &parsed) == nil {
				return parsed, nil
			}
			return map[string]any{"answer": text}, nil
		}
	}
	return result, nil
}

func searchParallel(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("parallel")
	if key == "" {
		value, err := mcpCall(ctx, runtime, "parallel", runtime.endpoint("parallel", "https://search.parallel.ai/mcp"), http.Header{"Accept": []string{"application/json, text/event-stream"}}, "tools/call", map[string]any{
			"name": "web_search",
			"arguments": map[string]any{
				"objective":      request.Query,
				"search_queries": []string{request.Query},
				"mode":           "fast",
			},
		})
		if err != nil {
			return SearchResponse{}, err
		}
		data, _ := json.Marshal(value)
		response, err := genericJSONResponse("parallel", data, request.NumSearchResult, "keyless")
		if err != nil {
			return SearchResponse{}, err
		}
		return response, nil
	}
	headers := http.Header{"Accept": []string{"application/json"}, "x-api-key": []string{key}, "parallel-beta": []string{"search-extract-2025-10-10"}}
	data, _, err := runtime.postJSON(ctx, "parallel", runtime.endpoint("parallel", "https://api.parallel.ai/v1beta/search"), headers, map[string]any{
		"objective":      request.Query,
		"search_queries": []string{request.Query},
		"mode":           "fast",
		"excerpts":       map[string]any{"max_chars_per_result": 10000},
	}, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("parallel", data, request.NumSearchResult, "api_key")
}

func searchExa(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("exa")
	if key == "" {
		query := url.Values{"tools": {"web_search_exa"}}
		target := runtime.endpoint("exa", "https://mcp.exa.ai/mcp") + "?" + query.Encode()
		value, err := mcpCall(ctx, runtime, "exa", target, http.Header{"Accept": []string{"application/json, text/event-stream"}, "x-exa-source": []string{"agentsd"}}, "tools/call", map[string]any{
			"name": "web_search_exa",
			"arguments": map[string]any{
				"query":      request.Query,
				"numResults": request.NumSearchResult,
			},
		})
		if err != nil {
			return SearchResponse{}, err
		}
		data, _ := json.Marshal(value)
		return genericJSONResponse("exa", data, request.NumSearchResult, "keyless")
	}
	data, _, err := runtime.postJSON(ctx, "exa", runtime.endpoint("exa", "https://api.exa.ai/search"), http.Header{"x-api-key": []string{key}}, map[string]any{
		"query":      request.Query,
		"numResults": request.NumSearchResult,
		"type":       "auto",
		"contents":   map[string]any{"summary": map[string]any{"query": request.Query}},
	}, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("exa", data, request.NumSearchResult, "api_key")
}

func searchFirecrawl(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	headers := http.Header{"Accept": []string{"application/json"}}
	authMode := "keyless"
	if key := runtime.bearer("firecrawl"); key != "" {
		headers.Set("Authorization", "Bearer "+key)
		authMode = "api_key"
	}
	payload := map[string]any{"query": request.Query, "limit": request.NumSearchResult, "sources": []any{map[string]any{"type": "web"}}}
	if request.Recency != "" {
		payload["tbs"] = map[string]string{"day": "qdr:d", "week": "qdr:w", "month": "qdr:m", "year": "qdr:y"}[request.Recency]
	}
	data, _, err := runtime.postJSON(ctx, "firecrawl", providerEndpoint(runtime, "firecrawl", "https://api.firecrawl.dev/v2", "/search"), headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("firecrawl", data, request.NumSearchResult, authMode)
}

func searchPerplexity(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	entry := runtime.authEntry("perplexity")
	if key := runtime.bearer("perplexity"); key != "" {
		target := runtime.endpoint("perplexity", "https://api.perplexity.ai") + "/chat/completions"
		payload := map[string]any{
			"model":    providerModel(runtime, "perplexity", "sonar"),
			"messages": []map[string]string{{"role": "user", "content": request.Query}},
			"web_search_options": map[string]any{
				"search_context_size": "high",
			},
		}
		if request.MaxTokens > 0 {
			payload["max_tokens"] = request.MaxTokens
		}
		data, _, err := runtime.postJSON(ctx, "perplexity", target, http.Header{"Authorization": []string{"Bearer " + key}}, payload, defaultMaxResponseBytes)
		if err != nil {
			return SearchResponse{}, err
		}
		return genericJSONResponse("perplexity", data, request.NumSearchResult, "api_key")
	}

	target := runtime.endpoint("perplexity", "https://www.perplexity.ai/rest/sse/perplexity_ask")
	headers := http.Header{
		"Accept":  []string{"text/event-stream"},
		"Origin":  []string{"https://www.perplexity.ai"},
		"Referer": []string{"https://www.perplexity.ai/"},
	}
	authMode := "anonymous"
	if entry.Cookies != "" {
		headers.Set("Cookie", entry.Cookies)
		authMode = "cookies"
	} else if entry.AccessToken != "" {
		headers.Set("Cookie", "__Secure-next-auth.session-token="+entry.AccessToken)
		authMode = "oauth"
	}
	payload := map[string]any{
		"query_str":                       request.Query,
		"frontend_uuid":                   fmt.Sprintf("%d", time.Now().UnixNano()),
		"frontend_context_uuid":           fmt.Sprintf("%d", time.Now().UnixNano()+1),
		"version":                         "2.18",
		"language":                        "en-US",
		"skip_search_enabled":             false,
		"always_search_override":          true,
		"send_back_text_in_streaming_api": true,
	}
	body, _, err := runtime.postJSON(ctx, "perplexity", target, headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return parseSSESearchResponse("perplexity", body, request.NumSearchResult, authMode)
}

func parseSSESearchResponse(provider string, data []byte, limit int, authMode string) (SearchResponse, error) {
	var sources []SearchSource
	var answerParts []string
	var requestID string
	var model string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var value any
		if json.Unmarshal([]byte(payload), &value) != nil {
			continue
		}
		collectJSONSources(value, &sources, 0)
		if record, ok := value.(map[string]any); ok {
			if text := stringValue(mapValue(record, "text", "answer", "output_text")); text != "" {
				answerParts = append(answerParts, text)
			}
			if requestID == "" {
				requestID = stringValue(mapValue(record, "request_id", "requestId", "id"))
			}
			if model == "" {
				model = stringValue(mapValue(record, "model", "display_model"))
			}
		}
	}
	response := SearchResponse{
		Provider:  provider,
		Answer:    strings.TrimSpace(strings.Join(answerParts, "")),
		Sources:   uniqueSources(sources, limit),
		RequestID: requestID,
		Model:     model,
		AuthMode:  authMode,
	}
	if !responseHasContent(response) {
		return SearchResponse{}, providerError(provider, 502, "SSE response contained no search result", nil)
	}
	return response, nil
}

func mcpCallWithSession(ctx context.Context, runtime *Runtime, provider, target string, headers http.Header, method string, params map[string]any) (any, http.Header, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      fmt.Sprintf("%s-%d", provider, time.Now().UnixNano()),
		"method":  method,
		"params":  params,
	}
	data, responseHeaders, err := runtime.postJSON(ctx, provider, target, headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return nil, nil, err
	}
	if len(data) == 0 && strings.HasPrefix(method, "notifications/") {
		return nil, responseHeaders, nil
	}
	response, err := parseMCPResponse(data)
	if err != nil {
		return nil, responseHeaders, providerError(provider, 502, "invalid MCP response", err)
	}
	if errorValue, ok := response["error"].(map[string]any); ok {
		return nil, responseHeaders, providerError(provider, 502, stringValue(errorValue["message"]), nil)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		return response, responseHeaders, nil
	}
	if structured := result["structuredContent"]; structured != nil {
		return structured, responseHeaders, nil
	}
	if content, ok := result["content"].([]any); ok {
		for _, item := range content {
			record, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text := stringValue(record["text"])
			if text == "" {
				continue
			}
			var parsed any
			if json.Unmarshal([]byte(text), &parsed) == nil {
				return parsed, responseHeaders, nil
			}
			return map[string]any{"answer": text}, responseHeaders, nil
		}
	}
	return result, responseHeaders, nil
}

func searchZAI(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("zai")
	if key == "" {
		return SearchResponse{}, providerError("zai", 401, "API key is not configured", nil)
	}
	target := runtime.endpoint("zai", "https://api.z.ai/api/mcp/web_search_prime/mcp")
	headers := http.Header{"Authorization": []string{"Bearer " + key}, "Accept": []string{"application/json, text/event-stream"}}
	_, responseHeaders, err := mcpCallWithSession(ctx, runtime, "zai", target, headers, "initialize", map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "agentsd", "version": "0.1"},
	})
	if err != nil {
		return SearchResponse{}, err
	}
	sessionID := responseHeaders.Get("Mcp-Session-Id")
	if sessionID != "" {
		headers.Set("Mcp-Session-Id", sessionID)
	}
	if _, _, err := mcpCallWithSession(ctx, runtime, "zai", target, headers, "notifications/initialized", map[string]any{}); err != nil {
		return SearchResponse{}, err
	}
	value, _, err := mcpCallWithSession(ctx, runtime, "zai", target, headers, "tools/call", map[string]any{
		"name": "web_search_prime",
		"arguments": map[string]any{
			"query": request.Query,
			"count": request.NumSearchResult,
		},
	})
	if err != nil {
		return SearchResponse{}, err
	}
	data, _ := json.Marshal(value)
	return genericJSONResponse("zai", data, request.NumSearchResult, "api_key")
}

func providerModel(runtime *Runtime, provider, fallback string) string {
	if model := strings.TrimSpace(runtime.providerConfig(provider).Model); model != "" {
		return model
	}
	if model := strings.TrimSpace(runtime.Model.RequestModelID); model != "" {
		return model
	}
	if model := strings.TrimSpace(runtime.Model.ModelID); model != "" {
		return model
	}
	return fallback
}

func providerEndpoint(runtime *Runtime, provider, fallback, suffix string) string {
	base := runtime.endpoint(provider, fallback)
	if strings.HasSuffix(base, suffix) {
		return base
	}
	return strings.TrimRight(base, "/") + suffix
}

func searchOpenAI(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	return searchResponsesProvider(ctx, runtime, request, "openai", "https://api.openai.com/v1", "web_search_preview")
}

func searchCodex(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	return searchResponsesProvider(ctx, runtime, request, "codex", "https://chatgpt.com/backend-api/codex", "web_search")
}

func searchXAI(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	return searchResponsesProvider(ctx, runtime, request, "xai", "https://api.x.ai/v1", "web_search")
}

func searchResponsesProvider(ctx context.Context, runtime *Runtime, request SearchRequest, provider, fallback, toolType string) (SearchResponse, error) {
	key := runtime.bearer(provider)
	if key == "" {
		return SearchResponse{}, providerError(provider, 401, "credential is not configured", nil)
	}
	payload := map[string]any{
		"model": providerModel(runtime, provider, map[string]string{
			"openai": "gpt-4.1-mini",
			"codex":  "gpt-5-codex",
			"xai":    "grok-3-mini",
		}[provider]),
		"input": request.Query,
		"tools": []any{map[string]any{"type": toolType}},
	}
	if request.MaxTokens > 0 {
		payload["max_output_tokens"] = request.MaxTokens
	}
	headers := http.Header{"Authorization": []string{"Bearer " + key}, "Accept": []string{"application/json"}}
	data, _, err := runtime.postJSON(ctx, provider, providerEndpoint(runtime, provider, fallback, "/responses"), headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse(provider, data, request.NumSearchResult, authModeFor(runtime, provider))
}

func searchOpenRouter(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("openrouter")
	if key == "" {
		return SearchResponse{}, providerError("openrouter", 401, "API key is not configured", nil)
	}
	payload := map[string]any{
		"model":    providerModel(runtime, "openrouter", "openai/gpt-4o-mini"),
		"messages": []map[string]string{{"role": "user", "content": request.Query}},
		"plugins":  []any{map[string]any{"id": "web", "max_results": request.NumSearchResult}},
	}
	headers := http.Header{"Authorization": []string{"Bearer " + key}, "Accept": []string{"application/json"}}
	data, _, err := runtime.postJSON(ctx, "openrouter", providerEndpoint(runtime, "openrouter", "https://openrouter.ai/api/v1", "/chat/completions"), headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("openrouter", data, request.NumSearchResult, "api_key")
}

func searchAnthropic(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	key := runtime.bearer("anthropic")
	if key == "" {
		return SearchResponse{}, providerError("anthropic", 401, "credential is not configured", nil)
	}
	payload := map[string]any{
		"model":      providerModel(runtime, "anthropic", "claude-3-5-sonnet-latest"),
		"max_tokens": request.MaxTokens,
		"messages":   []map[string]string{{"role": "user", "content": request.Query}},
		"tools": []any{map[string]any{
			"type": "web_search_20250305",
			"name": "web_search",
		}},
	}
	if request.MaxTokens <= 0 {
		payload["max_tokens"] = 4096
	}
	headers := http.Header{
		"x-api-key":         []string{key},
		"anthropic-version": []string{"2023-06-01"},
		"Accept":            []string{"application/json"},
	}
	data, _, err := runtime.postJSON(ctx, "anthropic", providerEndpoint(runtime, "anthropic", "https://api.anthropic.com/v1", "/messages"), headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("anthropic", data, request.NumSearchResult, authModeFor(runtime, "anthropic"))
}

func searchGemini(ctx context.Context, runtime *Runtime, request SearchRequest) (SearchResponse, error) {
	entry := runtime.authEntry("gemini")
	key := runtime.bearer("gemini")
	if key == "" {
		return SearchResponse{}, providerError("gemini", 401, "credential is not configured", nil)
	}
	model := providerModel(runtime, "gemini", "gemini-2.0-flash")
	target := providerEndpoint(runtime, "gemini", "https://generativelanguage.googleapis.com/v1beta", "/models/"+url.PathEscape(model)+":generateContent")
	parsed, err := url.Parse(target)
	if err != nil {
		return SearchResponse{}, err
	}
	headers := http.Header{"Accept": []string{"application/json"}}
	if strings.TrimSpace(entry.APIKey) != "" {
		query := parsed.Query()
		query.Set("key", key)
		parsed.RawQuery = query.Encode()
	} else {
		headers.Set("Authorization", "Bearer "+key)
	}
	payload := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": request.Query}}}},
		"tools":    []any{map[string]any{"google_search": map[string]any{}}},
	}
	data, _, err := runtime.postJSON(ctx, "gemini", parsed.String(), headers, payload, defaultMaxResponseBytes)
	if err != nil {
		return SearchResponse{}, err
	}
	return genericJSONResponse("gemini", data, request.NumSearchResult, authModeFor(runtime, "gemini"))
}
