package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"agentsd/internal/agent"
	"agentsd/internal/llm"
)

type testRunner struct {
	wait bool
}

func (runner testRunner) Run(ctx context.Context, request agent.RunRequest) (agent.RunResult, error) {
	if runner.wait {
		<-ctx.Done()
		return agent.RunResult{}, ctx.Err()
	}
	return agent.RunResult{FinalMessage: llm.Message{Role: llm.RoleAssistant, Content: "done"}, Turns: 1}, nil
}

func TestServerSpawnAndInspect(t *testing.T) {
	factory := func(string) (Runner, string, error) {
		return testRunner{}, "test-model", nil
	}
	manager, err := NewManager(factory)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)
	socketPath := filepath.Join(t.TempDir(), "agentsd.sock")
	listener, err := ListenUnix(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()

	params, err := json.Marshal(map[string]string{"task": "test task"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Call(context.Background(), socketPath, Request{Method: "spawn", Params: params})
	if err != nil {
		t.Fatal(err)
	}
	var spawned AgentSummary
	if err := json.Unmarshal(result, &spawned); err != nil {
		t.Fatal(err)
	}
	if spawned.ID != "a0001" || spawned.Status != "running" {
		t.Fatalf("spawned = %#v", spawned)
	}

	deadline := time.Now().Add(time.Second)
	for {
		result, err = Call(context.Background(), socketPath, Request{Method: "inspect", Params: mustJSON(map[string]string{"id": spawned.ID})})
		if err != nil {
			t.Fatal(err)
		}
		var inspected AgentSummary
		if err := json.Unmarshal(result, &inspected); err != nil {
			t.Fatal(err)
		}
		if inspected.Status == "completed" {
			if inspected.Final != "done" {
				t.Fatalf("inspected = %#v", inspected)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent did not complete: %#v", inspected)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestManagerKillCancelsRunner(t *testing.T) {
	manager, err := NewManager(func(string) (Runner, string, error) {
		return testRunner{wait: true}, "test-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	spawned, err := manager.Spawn("", "wait")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Kill(spawned.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		inspected, err := manager.Inspect(spawned.ID)
		if err != nil {
			t.Fatal(err)
		}
		if inspected.Status == "canceled" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent was not canceled: %#v", inspected)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCallReturnsDaemonError(t *testing.T) {
	factory := func(string) (Runner, string, error) { return testRunner{}, "test-model", nil }
	manager, err := NewManager(factory)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)
	socketPath := filepath.Join(t.TempDir(), "agentsd.sock")
	listener, err := ListenUnix(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()
	_, err = Call(context.Background(), socketPath, Request{Method: "missing"})
	if err == nil || err.Error() != "daemon: unknown method missing" {
		t.Fatalf("error = %v", err)
	}
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
