package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type AuthStore map[string]ProviderAuth

type ProviderAuth struct {
	APIKey string `json:"api_key,omitempty"`
}

func LoadAuth(path string) (AuthStore, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return AuthStore{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: read auth file %q: %w", path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var auth AuthStore
	if err := decoder.Decode(&auth); err != nil {
		return nil, fmt.Errorf("config: parse auth file %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("config: auth file %q contains multiple JSON values", path)
		}
		return nil, fmt.Errorf("config: parse auth file %q: %w", path, err)
	}
	if auth == nil {
		auth = AuthStore{}
	}
	return auth, nil
}

func (auth AuthStore) APIKey(providerID string) string {
	entry, ok := auth[providerID]
	if !ok || strings.TrimSpace(entry.APIKey) == "" {
		return ""
	}
	return entry.APIKey
}

func SaveAuth(path string, auth AuthStore) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("config: create auth directory %q: %w", parent, err)
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode auth file: %w", err)
	}
	data = append(data, '\n')

	file, err := os.CreateTemp(parent, ".auth-*.tmp")
	if err != nil {
		return fmt.Errorf("config: create temporary auth file: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("config: set auth file permissions: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("config: write auth file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("config: sync auth file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("config: close auth file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("config: replace auth file %q: %w", path, err)
	}
	return nil
}
