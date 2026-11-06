package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func Call(ctx context.Context, socketPath string, request Request) (json.RawMessage, error) {
	if request.Method == "" {
		return nil, fmt.Errorf("daemon: request method is required")
	}
	dialer := net.Dialer{}
	connection, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("daemon: connect %q: %w", socketPath, err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return nil, fmt.Errorf("daemon: set request deadline: %w", err)
		}
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return nil, fmt.Errorf("daemon: send request: %w", err)
	}
	var response rpcResponse
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return nil, fmt.Errorf("daemon: read response: %w", err)
	}
	if response.Error != "" {
		return nil, errors.New(response.Error)
	}
	return response.Result, nil
}

func WebSocketSocketPath(socketPath string) string {
	return socketPath + ".ws"
}

func ListenUnix(socketPath string) (net.Listener, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("daemon: socket path is required")
	}
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, fmt.Errorf("daemon: create socket directory: %w", err)
	}
	if _, err := os.Stat(socketPath); err == nil {
		connection, dialErr := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
		if dialErr == nil {
			_ = connection.Close()
			return nil, fmt.Errorf("daemon: socket %q is already in use", socketPath)
		}
		if removeErr := os.Remove(socketPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return nil, fmt.Errorf("daemon: remove stale socket: %w", removeErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("daemon: inspect socket: %w", err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("daemon: listen on %q: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return nil, fmt.Errorf("daemon: set socket permissions: %w", err)
	}
	return listener, nil
}
