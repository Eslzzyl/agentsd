package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAuthLoadMissingAndSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	auth, err := LoadAuth(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(auth) != 0 {
		t.Fatalf("missing auth store = %#v, want empty", auth)
	}

	want := AuthStore{
		"gateway": {APIKey: "secret-key"},
		"openai":  {APIKey: "openai-key"},
	}
	if err := SaveAuth(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAuth(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey("gateway") != "secret-key" || got.APIKey("openai") != "openai-key" || got.APIKey("missing") != "" {
		t.Fatalf("loaded provider keys = %#v", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if gotMode := info.Mode().Perm(); gotMode != 0o600 {
			t.Fatalf("auth file mode = %04o, want 0600", gotMode)
		}
	}
}

func TestAuthStoreResolvesBearerAndExtendedCredentials(t *testing.T) {
	auth := AuthStore{
		"oauth": {AccessToken: "access-token", RefreshToken: "refresh-token", ProjectID: "project"},
		"key":   {APIKey: "api-key"},
	}
	if got := auth.Bearer("oauth"); got != "access-token" {
		t.Fatalf("oauth bearer = %q", got)
	}
	if got := auth.Bearer("key"); got != "api-key" {
		t.Fatalf("api key bearer = %q", got)
	}
	if got := auth.Provider("oauth"); got.RefreshToken != "refresh-token" || got.ProjectID != "project" {
		t.Fatalf("extended credentials = %#v", got)
	}
}

func TestLoadAuthRejectsMalformedAndUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	for name, contents := range map[string]string{
		"malformed":       `{"gateway":`,
		"unknown field":   `{"gateway":{"token":"secret"}}`,
		"multiple values": `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadAuth(path); err == nil {
				t.Fatal("invalid auth file was accepted")
			}
		})
	}
}

func TestDefaultPathsUseAgentsdHomeDirectory(t *testing.T) {
	home := t.TempDir()
	paths := PathsForHome(home)
	if paths.Directory != filepath.Join(home, ".agentsd") || paths.ConfigFile != filepath.Join(home, ".agentsd", "config.toml") || paths.AuthFile != filepath.Join(home, ".agentsd", "auth.json") {
		t.Fatalf("paths = %#v", paths)
	}
	if err := paths.EnsureDirectory(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(paths.Directory); err != nil || !info.IsDir() {
		t.Fatalf("config directory stat = %v, error = %v", info, err)
	}
}
