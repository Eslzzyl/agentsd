package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type DaemonStatus struct {
	State               string    `json:"state"`
	PID                 int       `json:"pid"`
	StartedAt           time.Time `json:"started_at"`
	SocketPath          string    `json:"socket"`
	WebSocketSocketPath string    `json:"websocket_socket"`
	Workspace           string    `json:"workspace"`
	Executable          string    `json:"executable"`
}

type DaemonState struct {
	PID                 int       `json:"pid"`
	StartedAt           time.Time `json:"started_at"`
	SocketPath          string    `json:"socket"`
	WebSocketSocketPath string    `json:"websocket_socket"`
	Workspace           string    `json:"workspace"`
	Executable          string    `json:"executable"`
}

func (state DaemonState) Status(lifecycleState string) DaemonStatus {
	return DaemonStatus{
		State:               lifecycleState,
		PID:                 state.PID,
		StartedAt:           state.StartedAt,
		SocketPath:          state.SocketPath,
		WebSocketSocketPath: state.WebSocketSocketPath,
		Workspace:           state.Workspace,
		Executable:          state.Executable,
	}
}

func WriteDaemonState(path string, state DaemonState) error {
	if path == "" {
		return fmt.Errorf("daemon: state path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("daemon: create state directory: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".daemon-state-*")
	if err != nil {
		return fmt.Errorf("daemon: create temporary state file: %w", err)
	}
	temporaryPath := file.Name()
	defer os.Remove(temporaryPath)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("daemon: set state permissions: %w", err)
	}
	if err := json.NewEncoder(file).Encode(state); err != nil {
		_ = file.Close()
		return fmt.Errorf("daemon: write state: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("daemon: sync state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("daemon: close state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("daemon: install state file: %w", err)
	}
	return nil
}

func ReadDaemonState(path string) (DaemonState, error) {
	if path == "" {
		return DaemonState{}, fmt.Errorf("daemon: state path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return DaemonState{}, err
	}
	defer file.Close()
	var state DaemonState
	if err := json.NewDecoder(file).Decode(&state); err != nil {
		return DaemonState{}, fmt.Errorf("daemon: read state: %w", err)
	}
	return state, nil
}

func RemoveDaemonState(path string) error {
	if path == "" {
		return fmt.Errorf("daemon: state path is required")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("daemon: remove state: %w", err)
	}
	return nil
}
