package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"agentsd/internal/config"

	"github.com/pelletier/go-toml/v2"
)

func runConfig(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agents config <show|providers|models|search|fetch|validate|set>")
	}
	switch args[0] {
	case "show":
		return runConfigShow(args[1:])
	case "providers":
		return runConfigProviders(args[1:])
	case "models":
		return runConfigModels(args[1:])
	case "search":
		return runConfigSearch(args[1:])
	case "fetch":
		return runConfigFetch(args[1:])
	case "validate":
		return runConfigValidate(args[1:])
	case "set":
		return runConfigSet(args[1:])
	default:
		return fmt.Errorf("usage: agents config <show|providers|models|search|fetch|validate|set>")
	}
}

func runConfigShow(args []string) error {
	path, jsonOutput, err := parseConfigOutputFlags("config show", args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printConfigJSON(cfg)
	}
	return printConfigTOML(cfg)
}

type providerSummary struct {
	ID      string   `json:"id"`
	APIType string   `json:"api_type"`
	BaseURL string   `json:"base_url,omitempty"`
	Models  []string `json:"models"`
}

func runConfigProviders(args []string) error {
	path, jsonOutput, err := parseConfigOutputFlags("config providers", args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	providers := make([]providerSummary, 0, len(cfg.Providers))
	for id, provider := range cfg.Providers {
		models := make([]string, 0, len(provider.Models))
		for modelID := range provider.Models {
			models = append(models, modelID)
		}
		sort.Strings(models)
		providers = append(providers, providerSummary{
			ID:      id,
			APIType: string(provider.APIType),
			BaseURL: provider.BaseURL,
			Models:  models,
		})
	}
	sort.Slice(providers, func(left, right int) bool { return providers[left].ID < providers[right].ID })
	if jsonOutput {
		return printConfigJSON(providers)
	}
	fmt.Printf("%-24s %-24s %-40s %s\n", "ID", "API TYPE", "BASE URL", "MODELS")
	for _, provider := range providers {
		fmt.Printf("%-24s %-24s %-40s %s\n", provider.ID, provider.APIType, provider.BaseURL, strings.Join(provider.Models, ","))
	}
	return nil
}

type modelSummary struct {
	Provider          string `json:"provider"`
	ID                string `json:"id"`
	APIType           string `json:"api_type"`
	BaseURL           string `json:"base_url,omitempty"`
	RequestModelID    string `json:"request_model_id"`
	ContextWindow     int    `json:"context_window"`
	MaxOutputTokens   int    `json:"max_output_tokens"`
	SupportsImages    bool   `json:"supports_images"`
	SupportsStreaming bool   `json:"supports_streaming"`
}

func runConfigModels(args []string) error {
	flags, path, jsonOutput := newConfigOutputFlags("config models")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return fmt.Errorf("usage: agents config models [provider] [options]")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	selectedProvider := ""
	if flags.NArg() == 1 {
		selectedProvider = flags.Arg(0)
		if _, ok := cfg.Providers[selectedProvider]; !ok {
			return fmt.Errorf("config: provider %q is not defined", selectedProvider)
		}
	}
	models := make([]modelSummary, 0)
	providerIDs := make([]string, 0, len(cfg.Providers))
	for providerID := range cfg.Providers {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	for _, providerID := range providerIDs {
		if selectedProvider != "" && providerID != selectedProvider {
			continue
		}
		provider := cfg.Providers[providerID]
		modelIDs := make([]string, 0, len(provider.Models))
		for modelID := range provider.Models {
			modelIDs = append(modelIDs, modelID)
		}
		sort.Strings(modelIDs)
		for _, modelID := range modelIDs {
			model := provider.Models[modelID]
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
				requestModelID = modelID
			}
			models = append(models, modelSummary{
				Provider:          providerID,
				ID:                modelID,
				APIType:           string(apiType),
				BaseURL:           baseURL,
				RequestModelID:    requestModelID,
				ContextWindow:     model.ContextWindow,
				MaxOutputTokens:   model.MaxOutputTokens,
				SupportsImages:    model.SupportsImages,
				SupportsStreaming: model.SupportsStreaming,
			})
		}
	}
	if *jsonOutput {
		return printConfigJSON(models)
	}
	fmt.Printf("%-20s %-32s %-24s %-12s %-12s\n", "PROVIDER", "MODEL", "API TYPE", "CONTEXT", "MAX OUTPUT")
	for _, model := range models {
		fmt.Printf("%-20s %-32s %-24s %-12d %-12d\n", model.Provider, model.ID, model.APIType, model.ContextWindow, model.MaxOutputTokens)
	}
	return nil
}

type searchConfigView struct {
	Enabled   bool                                   `toml:"enabled" json:"enabled"`
	Timeout   int                                    `toml:"timeout_seconds" json:"timeout_seconds"`
	MaxResult int                                    `toml:"max_results" json:"max_results"`
	Order     []string                               `toml:"order" json:"order"`
	Providers map[string]config.SearchProviderConfig `toml:"providers,omitempty" json:"providers,omitempty"`
}

type fetchConfigView struct {
	Enabled  bool  `toml:"enabled" json:"enabled"`
	Timeout  int   `toml:"timeout_seconds" json:"timeout_seconds"`
	MaxBytes int64 `toml:"max_bytes" json:"max_bytes"`
}

func runConfigSearch(args []string) error {
	path, jsonOutput, err := parseConfigOutputFlags("config search", args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	providers := cfg.Search.Providers
	if providers == nil {
		providers = map[string]config.SearchProviderConfig{}
	}
	view := struct {
		Search searchConfigView `toml:"search" json:"search"`
	}{Search: searchConfigView{
		Enabled:   cfg.Search.IsEnabled(),
		Timeout:   cfg.Search.TimeoutSeconds(),
		MaxResult: cfg.Search.MaxResults(),
		Order:     cfg.Search.ProviderOrder(),
		Providers: providers,
	}}
	return printConfigView(view, jsonOutput)
}

func runConfigFetch(args []string) error {
	path, jsonOutput, err := parseConfigOutputFlags("config fetch", args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	view := struct {
		Fetch fetchConfigView `toml:"fetch" json:"fetch"`
	}{Fetch: fetchConfigView{
		Enabled:  cfg.Fetch.IsEnabled(),
		Timeout:  cfg.Fetch.TimeoutSeconds(),
		MaxBytes: cfg.Fetch.MaxBodyBytes(),
	}}
	return printConfigView(view, jsonOutput)
}

type resolvedConfigView struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	RequestModelID  string `json:"request_model_id"`
	APIType         string `json:"api_type"`
	BaseURL         string `json:"base_url"`
	ThinkingLevel   string `json:"thinking_level"`
	ContextWindow   int    `json:"context_window"`
	MaxOutputTokens int    `json:"max_output_tokens"`
}

func runConfigValidate(args []string) error {
	path, jsonOutput, err := parseConfigOutputFlags("config validate", args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	resolved, err := cfg.Resolve()
	if err != nil {
		return err
	}
	view := resolvedConfigView{
		Provider:        resolved.ProviderID,
		Model:           resolved.ModelID,
		RequestModelID:  resolved.RequestModelID,
		APIType:         string(resolved.APIType),
		BaseURL:         resolved.BaseURL,
		ThinkingLevel:   resolved.ThinkingLevel.String(),
		ContextWindow:   resolved.ContextWindow,
		MaxOutputTokens: resolved.MaxOutputTokens,
	}
	if jsonOutput {
		return printConfigJSON(map[string]any{"valid": true, "resolved": view})
	}
	fmt.Printf("valid: provider=%s model=%s request_model_id=%s api_type=%s\n", view.Provider, view.Model, view.RequestModelID, view.APIType)
	return nil
}

func runConfigSet(args []string) error {
	flags, path, jsonOutput := newConfigOutputFlags("config set")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		return fmt.Errorf("usage: agents config set [--config path] <path> <value>")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	updated, err := config.SetConfigValue(cfg, flags.Arg(0), flags.Arg(1))
	if err != nil {
		return err
	}
	if err := config.SaveConfig(*path, updated); err != nil {
		return err
	}
	if *jsonOutput {
		return printConfigJSON(map[string]any{"updated": flags.Arg(0), "config": updated})
	}
	fmt.Printf("updated %s\n", flags.Arg(0))
	return nil
}

func parseConfigOutputFlags(name string, args []string) (string, bool, error) {
	flags, path, jsonOutput := newConfigOutputFlags(name)
	if err := flags.Parse(args); err != nil {
		return "", false, err
	}
	if flags.NArg() != 0 {
		return "", false, fmt.Errorf("usage: agents %s [options]", name[len("config "):])
	}
	absolute, err := filepath.Abs(*path)
	if err != nil {
		return "", false, err
	}
	return absolute, *jsonOutput, nil
}

func newConfigOutputFlags(name string) (*flag.FlagSet, *string, *bool) {
	paths, err := config.DefaultPaths()
	defaultPath := filepath.Join(os.Getenv("HOME"), ".agentsd", "config.toml")
	if err == nil {
		defaultPath = paths.ConfigFile
	}
	path := defaultPath
	jsonOutput := false
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&path, "config", defaultPath, "config TOML path")
	flags.BoolVar(&jsonOutput, "json", false, "print JSON")
	return flags, &path, &jsonOutput
}

func printConfigJSON(value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func printConfigTOML(value any) error {
	encoded, err := toml.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Print(string(encoded))
	return nil
}

func printConfigView(value any, jsonOutput bool) error {
	if jsonOutput {
		return printConfigJSON(value)
	}
	return printConfigTOML(value)
}

func runAuth(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: agents auth <list|set|remove>")
	}
	switch args[0] {
	case "list":
		return runAuthList(args[1:])
	case "set":
		return runAuthSet(args[1:])
	case "remove":
		return runAuthRemove(args[1:])
	default:
		return fmt.Errorf("usage: agents auth <list|set|remove>")
	}
}

type authSummary struct {
	Provider     string `json:"provider"`
	APIKey       bool   `json:"api_key"`
	AccessToken  bool   `json:"access_token"`
	RefreshToken bool   `json:"refresh_token"`
	Cookies      bool   `json:"cookies"`
	Username     bool   `json:"username"`
	Password     bool   `json:"password"`
	AccountID    bool   `json:"account_id"`
	ProjectID    bool   `json:"project_id"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
}

func runAuthList(args []string) error {
	path, jsonOutput, err := parseAuthOutputFlags("auth list", args)
	if err != nil {
		return err
	}
	auth, err := config.LoadAuth(path)
	if err != nil {
		return err
	}
	providers := make([]string, 0, len(auth))
	for provider := range auth {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	summaries := make([]authSummary, 0, len(providers))
	for _, provider := range providers {
		entry := auth[provider]
		summaries = append(summaries, authSummary{
			Provider:     provider,
			APIKey:       strings.TrimSpace(entry.APIKey) != "",
			AccessToken:  strings.TrimSpace(entry.AccessToken) != "",
			RefreshToken: strings.TrimSpace(entry.RefreshToken) != "",
			Cookies:      strings.TrimSpace(entry.Cookies) != "",
			Username:     strings.TrimSpace(entry.Username) != "",
			Password:     strings.TrimSpace(entry.Password) != "",
			AccountID:    strings.TrimSpace(entry.AccountID) != "",
			ProjectID:    strings.TrimSpace(entry.ProjectID) != "",
			ExpiresAt:    entry.ExpiresAt,
		})
	}
	if jsonOutput {
		return printConfigJSON(summaries)
	}
	fmt.Printf("%-20s %-8s %-12s %-12s %-8s %-8s %-8s\n", "PROVIDER", "API KEY", "TOKEN", "REFRESH", "COOKIES", "USER", "PASSWORD")
	for _, summary := range summaries {
		fmt.Printf("%-20s %-8t %-12t %-12t %-8t %-8t %-8t\n", summary.Provider, summary.APIKey, summary.AccessToken, summary.RefreshToken, summary.Cookies, summary.Username, summary.Password)
	}
	return nil
}

func runAuthSet(args []string) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("auth set", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	authPath := flags.String("auth", paths.AuthFile, "auth JSON path")
	field := flags.String("field", "", "credential field")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 || strings.TrimSpace(*field) == "" {
		return fmt.Errorf("usage: agents auth set [--auth path] --field <field> <provider>")
	}
	provider := flags.Arg(0)
	if !validAuthField(*field) {
		return fmt.Errorf("auth: unsupported field %q", *field)
	}
	stdinInfo, err := os.Stdin.Stat()
	if err != nil {
		return err
	}
	if stdinInfo.Mode()&os.ModeCharDevice != 0 {
		return fmt.Errorf("auth: pipe the credential value through stdin")
	}
	value, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	valueText := strings.TrimRight(string(value), "\r\n")
	if strings.TrimSpace(valueText) == "" {
		return fmt.Errorf("auth: credential value is empty")
	}
	path, err := filepath.Abs(*authPath)
	if err != nil {
		return err
	}
	auth, err := config.LoadAuth(path)
	if err != nil {
		return err
	}
	entry := auth[provider]
	if *field == "expires_at" {
		expiresAt, err := strconv.ParseInt(strings.TrimSpace(valueText), 10, 64)
		if err != nil {
			return fmt.Errorf("auth: expires_at must be an integer")
		}
		entry.ExpiresAt = expiresAt
	} else {
		setAuthStringField(&entry, *field, valueText)
	}
	auth[provider] = entry
	if err := config.SaveAuth(path, auth); err != nil {
		return err
	}
	if *jsonOutput {
		return printConfigJSON(map[string]any{"updated": provider, "field": *field})
	}
	fmt.Printf("updated %s.%s\n", provider, *field)
	return nil
}

func runAuthRemove(args []string) error {
	paths, err := config.DefaultPaths()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("auth remove", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	authPath := flags.String("auth", paths.AuthFile, "auth JSON path")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return fmt.Errorf("usage: agents auth remove [--auth path] <provider>")
	}
	path, err := filepath.Abs(*authPath)
	if err != nil {
		return err
	}
	auth, err := config.LoadAuth(path)
	if err != nil {
		return err
	}
	provider := flags.Arg(0)
	if _, ok := auth[provider]; !ok {
		return fmt.Errorf("auth: provider %q is not configured", provider)
	}
	delete(auth, provider)
	if err := config.SaveAuth(path, auth); err != nil {
		return err
	}
	if *jsonOutput {
		return printConfigJSON(map[string]any{"removed": provider})
	}
	fmt.Printf("removed %s\n", provider)
	return nil
}

func parseAuthOutputFlags(name string, args []string) (string, bool, error) {
	paths, err := config.DefaultPaths()
	if err != nil {
		return "", false, err
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	authPath := flags.String("auth", paths.AuthFile, "auth JSON path")
	jsonOutput := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(args); err != nil {
		return "", false, err
	}
	if flags.NArg() != 0 {
		return "", false, fmt.Errorf("usage: agents %s [options]", strings.TrimPrefix(name, "auth "))
	}
	path, err := filepath.Abs(*authPath)
	if err != nil {
		return "", false, err
	}
	return path, *jsonOutput, nil
}

func validAuthField(field string) bool {
	switch field {
	case "api_key", "access_token", "refresh_token", "cookies", "username", "password", "account_id", "project_id", "expires_at":
		return true
	default:
		return false
	}
}

func setAuthStringField(entry *config.ProviderAuth, field, value string) {
	switch field {
	case "api_key":
		entry.APIKey = value
	case "access_token":
		entry.AccessToken = value
	case "refresh_token":
		entry.RefreshToken = value
	case "cookies":
		entry.Cookies = value
	case "username":
		entry.Username = value
	case "password":
		entry.Password = value
	case "account_id":
		entry.AccountID = value
	case "project_id":
		entry.ProjectID = value
	}
}
