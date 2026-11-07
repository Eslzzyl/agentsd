package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetConfigValueUpdatesNestedAndDynamicPaths(t *testing.T) {
	cfg, err := ParseTOML([]byte(sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = SetConfigValue(cfg, "providers/gateway/models/gpt-6-luna/context_window", "128000")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers["gateway"].Models["gpt-6-luna"].ContextWindow; got != 128000 {
		t.Fatalf("context window = %d, want 128000", got)
	}
	cfg, err = SetConfigValue(cfg, "search/providers/brave/endpoint", "https://search.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Search.Providers["brave"].Endpoint; got != "https://search.example.test" {
		t.Fatalf("search endpoint = %q", got)
	}
	cfg, err = SetConfigValue(cfg, "search/order", "duckduckgo,brave")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Search.Order) != 2 || cfg.Search.Order[1] != "brave" {
		t.Fatalf("search order = %#v", cfg.Search.Order)
	}
}

func TestSetConfigValueRejectsUnknownAndInvalidFields(t *testing.T) {
	cfg, err := ParseTOML([]byte(sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetConfigValue(cfg, "unknown/value", "true"); err == nil {
		t.Fatal("unknown config path was accepted")
	}
	if _, err := SetConfigValue(cfg, "fetch/timeout_seconds", "slow"); err == nil {
		t.Fatal("invalid integer was accepted")
	}
}

func TestSaveConfigRoundTrip(t *testing.T) {
	cfg, err := ParseTOML([]byte(sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Provider != cfg.Provider || loaded.Model != cfg.Model {
		t.Fatalf("loaded config = %#v", loaded)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %04o, want 0600", info.Mode().Perm())
	}
}
