package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentsd/internal/config"
)

func TestRunConfigSetUpdatesConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := `provider = "gateway"
model = "model"

[providers.gateway]
api_type = "openai"

[providers.gateway.models.model]
context_window = 4096
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runConfigSet([]string{"--config", path, "providers/gateway/models/model/context_window", "8192"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers["gateway"].Models["model"].ContextWindow; got != 8192 {
		t.Fatalf("context window = %d, want 8192", got)
	}
}

func TestRunAuthListRedactsCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := config.SaveAuth(path, config.AuthStore{
		"gateway": {APIKey: "secret-api-key", AccountID: "account"},
	}); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	err = runAuthList([]string{"--auth", path})
	_ = writer.Close()
	os.Stdout = original
	output, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	text := string(output)
	if strings.Contains(text, "secret-api-key") || strings.Contains(text, "account") {
		t.Fatalf("auth output exposed credential values: %q", text)
	}
	if !strings.Contains(text, "gateway") || !strings.Contains(text, "true") {
		t.Fatalf("auth output omitted configured field: %q", text)
	}
}
