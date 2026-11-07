package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaemonStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.json")
	want := DaemonState{
		PID:                 42,
		StartedAt:           time.Date(2026, time.November, 7, 1, 2, 3, 0, time.UTC),
		SocketPath:          "/tmp/agentsd.sock",
		WebSocketSocketPath: "/tmp/agentsd.sock.ws",
		Workspace:           "/tmp/workspace",
		Executable:          "/tmp/agentsd",
	}
	if err := WriteDaemonState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDaemonState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("state = %#v, want %#v", got, want)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions = %o, want 600", info.Mode().Perm())
	}
	if err := RemoveDaemonState(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state after removal: %v", err)
	}
}
