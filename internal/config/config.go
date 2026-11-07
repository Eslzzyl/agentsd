package config

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"strings"

	"agentsd/internal/llm"
	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Provider      string                    `toml:"provider" json:"provider"`
	Model         string                    `toml:"model" json:"model"`
	ThinkingLevel string                    `toml:"thinking_level,omitempty" json:"thinking_level,omitempty"`
	Providers     map[string]ProviderConfig `toml:"providers" json:"providers"`
	Search        SearchConfig              `toml:"search,omitempty" json:"search,omitempty"`
	Fetch         FetchConfig               `toml:"fetch,omitempty" json:"fetch,omitempty"`
}

type SearchConfig struct {
	Enabled   *bool                           `toml:"enabled,omitempty" json:"enabled,omitempty"`
	Timeout   int                             `toml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`
	MaxResult int                             `toml:"max_results,omitempty" json:"max_results,omitempty"`
	Order     []string                        `toml:"order,omitempty" json:"order,omitempty"`
	Providers map[string]SearchProviderConfig `toml:"providers,omitempty" json:"providers,omitempty"`
}

type SearchProviderConfig struct {
	Enabled  *bool          `toml:"enabled,omitempty" json:"enabled,omitempty"`
	Endpoint string         `toml:"endpoint,omitempty" json:"endpoint,omitempty"`
	AuthRef  string         `toml:"auth_ref,omitempty" json:"auth_ref,omitempty"`
	Model    string         `toml:"model,omitempty" json:"model,omitempty"`
	Options  map[string]any `toml:"options,omitempty" json:"options,omitempty"`
}

type FetchConfig struct {
	Enabled  *bool `toml:"enabled,omitempty" json:"enabled,omitempty"`
	Timeout  int   `toml:"timeout_seconds,omitempty" json:"timeout_seconds,omitempty"`
	MaxBytes int64 `toml:"max_bytes,omitempty" json:"max_bytes,omitempty"`
}

const (
	DefaultSearchTimeoutSeconds = 60
	DefaultSearchMaxResults     = 10
	DefaultFetchTimeoutSeconds  = 30
	DefaultFetchMaxBytes        = 2 << 20
)

var DefaultSearchOrder = []string{
	"duckduckgo",
	"parallel",
	"exa",
	"firecrawl",
	"google",
	"ecosia",
	"startpage",
	"mojeek",
	"brave",
	"tavily",
	"jina",
	"kagi",
	"tinyfish",
	"searxng",
	"perplexity",
	"zai",
	"deepseek",
	"kimi",
	"synthetic",
	"ollama",
	"gemini",
	"anthropic",
	"codex",
	"xai",
	"openrouter",
	"openai",
}

func (cfg SearchConfig) IsEnabled() bool {
	return cfg.Enabled == nil || *cfg.Enabled
}

func (cfg SearchConfig) TimeoutSeconds() int {
	if cfg.Timeout <= 0 {
		return DefaultSearchTimeoutSeconds
	}
	return cfg.Timeout
}

func (cfg SearchConfig) MaxResults() int {
	if cfg.MaxResult <= 0 {
		return DefaultSearchMaxResults
	}
	return cfg.MaxResult
}

func (cfg SearchConfig) ProviderOrder() []string {
	if len(cfg.Order) == 0 {
		return append([]string(nil), DefaultSearchOrder...)
	}
	order := make([]string, 0, len(cfg.Order))
	for _, provider := range cfg.Order {
		if strings.TrimSpace(provider) != "" {
			order = append(order, strings.TrimSpace(provider))
		}
	}
	return order
}

func (cfg SearchConfig) Provider(id string) SearchProviderConfig {
	provider, ok := cfg.Providers[id]
	if !ok {
		return SearchProviderConfig{}
	}
	return provider
}

func (cfg SearchProviderConfig) IsEnabled() bool {
	return cfg.Enabled == nil || *cfg.Enabled
}

func (cfg FetchConfig) IsEnabled() bool {
	return cfg.Enabled == nil || *cfg.Enabled
}

func (cfg FetchConfig) TimeoutSeconds() int {
	if cfg.Timeout <= 0 {
		return DefaultFetchTimeoutSeconds
	}
	return cfg.Timeout
}

func (cfg FetchConfig) MaxBodyBytes() int64 {
	if cfg.MaxBytes <= 0 {
		return DefaultFetchMaxBytes
	}
	return cfg.MaxBytes
}

type ProviderConfig struct {
	APIType llm.APIType            `toml:"api_type" json:"api_type"`
	BaseURL string                 `toml:"base_url,omitempty" json:"base_url,omitempty"`
	Models  map[string]ModelConfig `toml:"models" json:"models"`
}

type ModelConfig struct {
	APIType           llm.APIType    `toml:"api_type,omitempty" json:"api_type,omitempty"`
	BaseURL           string         `toml:"base_url,omitempty" json:"base_url,omitempty"`
	RequestModelID    string         `toml:"request_model_id,omitempty" json:"request_model_id,omitempty"`
	ContextWindow     int            `toml:"context_window,omitempty" json:"context_window,omitempty"`
	MaxOutputTokens   int            `toml:"max_output_tokens,omitempty" json:"max_output_tokens,omitempty"`
	SupportsImages    bool           `toml:"supports_images,omitempty" json:"supports_images,omitempty"`
	SupportsStreaming bool           `toml:"supports_streaming,omitempty" json:"supports_streaming,omitempty"`
	ExtraBody         map[string]any `toml:"extra_body,omitempty" json:"extra_body,omitempty"`
}

type ResolvedModel struct {
	ProviderID        string
	ModelID           string
	RequestModelID    string
	APIType           llm.APIType
	BaseURL           string
	ThinkingLevel     llm.ThinkingLevelType
	ContextWindow     int
	MaxOutputTokens   int
	SupportsImages    bool
	SupportsStreaming bool
	ExtraBody         map[string]any
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %q: %w", path, err)
	}
	return ParseTOML(data)
}

func ParseTOML(data []byte) (Config, error) {
	var cfg Config
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse TOML: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	if strings.TrimSpace(cfg.Provider) == "" {
		return fmt.Errorf("config: provider is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return fmt.Errorf("config: model is required")
	}
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("config: at least one provider is required")
	}
	for providerID, provider := range cfg.Providers {
		if strings.TrimSpace(providerID) == "" {
			return fmt.Errorf("config: provider ID is required")
		}
		if !validAPIType(provider.APIType) {
			return fmt.Errorf("config: provider %q has invalid api_type %q", providerID, provider.APIType)
		}
		if err := validateBaseURL(provider.BaseURL); err != nil {
			return fmt.Errorf("config: provider %q: %w", providerID, err)
		}
		if len(provider.Models) == 0 {
			return fmt.Errorf("config: provider %q must define at least one model", providerID)
		}
		for modelID, model := range provider.Models {
			if strings.TrimSpace(modelID) == "" {
				return fmt.Errorf("config: provider %q has an empty model ID", providerID)
			}
			if model.APIType != "" && !validAPIType(model.APIType) {
				return fmt.Errorf("config: model %q in provider %q has invalid api_type %q", modelID, providerID, model.APIType)
			}
			if err := validateBaseURL(model.BaseURL); err != nil {
				return fmt.Errorf("config: model %q in provider %q: %w", modelID, providerID, err)
			}
			if model.ContextWindow < 0 || model.MaxOutputTokens < 0 {
				return fmt.Errorf("config: model %q in provider %q has negative token limits", modelID, providerID)
			}
		}
	}
	provider, ok := cfg.Providers[cfg.Provider]
	if !ok {
		return fmt.Errorf("config: selected provider %q is not defined", cfg.Provider)
	}
	selectedModel, ok := provider.Models[cfg.Model]
	if !ok {
		return fmt.Errorf("config: selected model %q is not defined for provider %q", cfg.Model, cfg.Provider)
	}
	if selectedModel.ContextWindow <= 0 {
		return fmt.Errorf("config: selected model %q must define a positive context_window", cfg.Model)
	}
	return nil
}

func (cfg Config) Resolve() (ResolvedModel, error) {
	if err := cfg.Validate(); err != nil {
		return ResolvedModel{}, err
	}
	provider := cfg.Providers[cfg.Provider]
	model := provider.Models[cfg.Model]
	apiType := provider.APIType
	if model.APIType != "" {
		apiType = model.APIType
	}
	baseURL := provider.BaseURL
	if model.BaseURL != "" {
		baseURL = model.BaseURL
	}
	requestModelID := model.RequestModelID
	if requestModelID == "" {
		requestModelID = cfg.Model
	}
	thinkingLevel := (ThinkingMatcher{}).MatchForModel(requestModelID)
	if cfg.ThinkingLevel != "" {
		thinkingLevel = (ThinkingMatcher{}).CoerceSaved(cfg.ThinkingLevel, requestModelID)
	}
	return ResolvedModel{
		ProviderID:        cfg.Provider,
		ModelID:           cfg.Model,
		RequestModelID:    requestModelID,
		APIType:           apiType,
		BaseURL:           baseURL,
		ThinkingLevel:     thinkingLevel,
		ContextWindow:     model.ContextWindow,
		MaxOutputTokens:   model.MaxOutputTokens,
		SupportsImages:    model.SupportsImages,
		SupportsStreaming: model.SupportsStreaming,
		ExtraBody:         llm.MergeExtraBody(model.ExtraBody, nil),
	}, nil
}

func (model ResolvedModel) MergedExtraBody() map[string]any {
	thinkingBody, _ := model.ThinkingLevel.ExtraBodyForAPI(model.APIType)
	return llm.MergeExtraBody(model.ExtraBody, thinkingBody)
}

func (model ResolvedModel) ThinkingConfig() (map[string]any, bool) {
	return model.ThinkingLevel.ThinkingConfigForAPI(model.APIType)
}

func validAPIType(apiType llm.APIType) bool {
	switch apiType {
	case llm.APITypeOpenAI, llm.APITypeOpenAIResponses, llm.APITypeOpenAIWebSocket, llm.APITypeAnthropic, llm.APITypeGoogle:
		return true
	default:
		return false
	}
}

func validateBaseURL(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		return fmt.Errorf("base_url must be an absolute HTTP or WebSocket URL")
	}
	return nil
}
