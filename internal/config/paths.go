package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	Directory         string
	ConfigFile        string
	AuthFile          string
	SessionsDirectory string
	StateFile         string
	LogFile           string
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("config: determine home directory: %w", err)
	}
	if home == "" {
		return Paths{}, fmt.Errorf("config: home directory is empty")
	}
	return PathsForHome(home), nil
}

func PathsForHome(home string) Paths {
	directory := filepath.Join(home, ".agentsd")
	return Paths{
		Directory:         directory,
		ConfigFile:        filepath.Join(directory, "config.toml"),
		AuthFile:          filepath.Join(directory, "auth.json"),
		SessionsDirectory: filepath.Join(directory, "sessions"),
		StateFile:         filepath.Join(directory, "daemon.json"),
		LogFile:           filepath.Join(directory, "daemon.log"),
	}
}

func (paths Paths) EnsureDirectory() error {
	if err := os.MkdirAll(paths.Directory, 0o700); err != nil {
		return fmt.Errorf("config: create directory %q: %w", paths.Directory, err)
	}
	return nil
}
