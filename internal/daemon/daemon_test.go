package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"agentsd/internal/agent"
	"agentsd/internal/llm"
	"agentsd/internal/session"

	"github.com/google/uuid"
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

	params, err := json.Marshal(map[string]string{"namespace": "test", "task": "test task"})
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
	if _, err := uuid.Parse(spawned.ID); err != nil || spawned.Namespace != "test" || spawned.Status != "running" {
		t.Fatalf("spawned = %#v", spawned)
	}

	deadline := time.Now().Add(time.Second)
	for {
		result, err = Call(context.Background(), socketPath, Request{Method: "inspect", Params: mustJSON(map[string]string{"namespace": "test", "id": spawned.ID})})
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
	spawned, err := manager.SpawnInNamespace("test", "", "wait")
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

func TestManagerPersistsNamespacesAndReloadsSessions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	factory := func(string) (Runner, string, error) {
		return testRunner{}, "test-model", nil
	}
	manager, err := NewManager(factory, store)
	if err != nil {
		t.Fatal(err)
	}
	spawned, err := manager.SpawnInNamespace("backend", "", "persist task")
	if err != nil {
		t.Fatal(err)
	}
	if spawned.Namespace != "backend" {
		t.Fatalf("spawned namespace = %q", spawned.Namespace)
	}
	deadline := time.Now().Add(time.Second)
	for {
		inspected, err := manager.InspectInNamespace("backend", spawned.ID[:8])
		if err != nil {
			t.Fatal(err)
		}
		if inspected.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent did not complete: %#v", inspected)
		}
		time.Sleep(time.Millisecond)
	}
	if got := len(manager.ListInNamespace("other")); got != 0 {
		t.Fatalf("other namespace count = %d", got)
	}
	if got := len(manager.ListAll()); got != 1 {
		t.Fatalf("all namespace count = %d", got)
	}

	reloaded, err := NewManager(factory, store)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := reloaded.Inspect("backend/" + spawned.ID[:8])
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "completed" || recovered.Final != "done" {
		t.Fatalf("recovered = %#v", recovered)
	}
}

func TestServerNamespaceCommands(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(func(string) (Runner, string, error) {
		return testRunner{}, "test-model", nil
	}, store)
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

	result, err := Call(context.Background(), socketPath, Request{
		Method: "namespace.create",
		Params: mustJSON(map[string]string{"name": "frontend"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var created NamespaceResult
	if err := json.Unmarshal(result, &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "frontend" {
		t.Fatalf("created namespace = %#v", created)
	}
	if _, err := Call(context.Background(), socketPath, Request{
		Method: "namespace.create",
		Params: mustJSON(map[string]string{"name": "frontend"}),
	}); err == nil {
		t.Fatal("duplicate namespace creation was accepted")
	}
	result, err = Call(context.Background(), socketPath, Request{Method: "namespace.list"})
	if err != nil {
		t.Fatal(err)
	}
	var namespaces []string
	if err := json.Unmarshal(result, &namespaces); err != nil {
		t.Fatal(err)
	}
	if len(namespaces) != 1 || namespaces[0] != "frontend" {
		t.Fatalf("namespaces = %#v", namespaces)
	}
	if _, err := Call(context.Background(), socketPath, Request{
		Method: "namespace.rename",
		Params: mustJSON(map[string]string{"from": "frontend", "to": "web"}),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(session.Header{Namespace: "web", Task: "remove"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Call(context.Background(), socketPath, Request{
		Method: "namespace.delete",
		Params: mustJSON(map[string]any{"name": "web", "force": true}),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestServerSpawnRequiresNamespace(t *testing.T) {
	manager, err := NewManager(func(string) (Runner, string, error) {
		return testRunner{}, "test-model", nil
	})
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

	_, err = Call(context.Background(), socketPath, Request{
		Method: "spawn",
		Params: mustJSON(map[string]string{"task": "missing namespace"}),
	})
	if err == nil || err.Error() != "session: namespace is required" {
		t.Fatalf("error = %v", err)
	}
}

func TestManagerForceDeleteCancelsActiveSessions(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(func(string) (Runner, string, error) {
		return testRunner{wait: true}, "test-model", nil
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SpawnInNamespace("running", "", "remove me"); err != nil {
		t.Fatal(err)
	}
	if err := manager.DeleteNamespace("running", true); err != nil {
		t.Fatal(err)
	}
	if got := manager.ListAll(); len(got) != 0 {
		t.Fatalf("agents after force delete = %#v", got)
	}
	namespaces, err := store.ListNamespaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(namespaces) != 0 {
		t.Fatalf("namespaces after force delete = %#v", namespaces)
	}
}
