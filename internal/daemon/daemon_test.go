package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentsd/internal/agent"
	"agentsd/internal/llm"
	"agentsd/internal/session"
	"agentsd/internal/tools"

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

type messageRunner struct {
	calls chan []llm.Message
}

func (runner *messageRunner) Run(_ context.Context, request agent.RunRequest) (agent.RunResult, error) {
	messages := append([]llm.Message(nil), request.Messages...)
	assistant := llm.Message{Role: llm.RoleAssistant, Content: "done"}
	messages = append(messages, assistant)
	runner.calls <- messages
	return agent.RunResult{FinalMessage: assistant, Messages: messages, Turns: 1}, nil
}

type blockingMessageRunner struct {
	started chan struct{}
	release chan struct{}
}

func (runner *blockingMessageRunner) Run(ctx context.Context, request agent.RunRequest) (agent.RunResult, error) {
	runner.started <- struct{}{}
	select {
	case <-runner.release:
	case <-ctx.Done():
		return agent.RunResult{}, ctx.Err()
	}
	messages := append([]llm.Message(nil), request.Messages...)
	assistant := llm.Message{Role: llm.RoleAssistant, Content: "done"}
	messages = append(messages, assistant)
	return agent.RunResult{FinalMessage: assistant, Messages: messages, Turns: 1}, nil
}

type controlledRunner struct {
	started  chan struct{}
	received chan agent.ControlRequest
	release  chan struct{}
}

func (runner *controlledRunner) Run(ctx context.Context, request agent.RunRequest) (agent.RunResult, error) {
	close(runner.started)
	select {
	case control := <-request.Control:
		runner.received <- control
		select {
		case <-runner.release:
			control.Complete(agent.ControlResult{Status: agent.ControlNoChange})
			return agent.RunResult{FinalMessage: llm.Message{Role: llm.RoleAssistant, Content: "done"}, Turns: 1}, nil
		case <-ctx.Done():
			return agent.RunResult{}, ctx.Err()
		}
	case <-ctx.Done():
		return agent.RunResult{}, ctx.Err()
	}
}

type contextEventRunner struct{}

func (contextEventRunner) Run(_ context.Context, request agent.RunRequest) (agent.RunResult, error) {
	if request.ContextEventSink != nil {
		request.ContextEventSink(agent.ContextEvent{
			Summary:          "summary",
			TokensBefore:     100,
			RemovedMessages:  2,
			KeptMessages:     3,
			CompactionNumber: 1,
		})
	}
	return agent.RunResult{FinalMessage: llm.Message{Role: llm.RoleAssistant, Content: "done"}, Turns: 1}, nil
}
func testSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "ad")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(directory)
	})
	return filepath.Join(directory, "s")
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
	socketPath := testSocketPath(t)
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
		if inspected.Status == "idle" {
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

func TestManagerDeliversSteerAndFollowUpMessagesAcrossTurns(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &messageRunner{calls: make(chan []llm.Message, 3)}
	manager, err := NewManager(func(string) (Runner, string, error) {
		return runner, "test-model", nil
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	spawned, err := manager.SpawnInNamespace("messages", "", "initial task")
	if err != nil {
		t.Fatal(err)
	}
	if first := <-runner.calls; len(first) != 3 || first[1].Content != "initial task" {
		t.Fatalf("first turn = %#v", first)
	}
	steer, err := manager.SendInNamespace("messages", spawned.ID, "change direction", tools.MessageModeSteer)
	if err != nil {
		t.Fatal(err)
	}
	if steer.Status != "queued" || steer.Mode != tools.MessageModeSteer {
		t.Fatalf("steer receipt = %#v", steer)
	}
	second := <-runner.calls
	if len(second) < 2 || second[len(second)-2].Content != "change direction" {
		t.Fatalf("steer turn = %#v", second)
	}
	followUp, err := manager.SendInNamespace("messages", spawned.ID, "summarize", tools.MessageModeFollowUp)
	if err != nil {
		t.Fatal(err)
	}
	if followUp.Status != "queued" || followUp.Mode != tools.MessageModeFollowUp {
		t.Fatalf("follow-up receipt = %#v", followUp)
	}
	third := <-runner.calls
	if len(third) < 2 || third[len(third)-2].Content != "summarize" {
		t.Fatalf("follow-up turn = %#v", third)
	}
	path, err := store.SessionPath("messages", spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"type":"message.queued"`) || !strings.Contains(text, `"type":"message.delivered"`) {
		t.Fatalf("session log = %s", text)
	}
	if _, err := manager.Kill(spawned.ID); err != nil {
		t.Fatal(err)
	}
}

func TestServerSessionSendAndInbox(t *testing.T) {
	runner := &blockingMessageRunner{
		started: make(chan struct{}, 2),
		release: make(chan struct{}),
	}
	manager, err := NewManager(func(string) (Runner, string, error) {
		return runner, "test-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)
	socketPath := testSocketPath(t)
	listener, err := ListenUnix(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()

	spawned, err := manager.SpawnInNamespace("messages", "", "task")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	result, err := Call(context.Background(), socketPath, Request{
		Method: "session.send",
		Params: mustJSON(map[string]any{
			"namespace": "messages",
			"id":        spawned.ID,
			"mode":      "steer",
			"content":   "inspect the failing test",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var receipt MessageReceipt
	if err := json.Unmarshal(result, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "queued" || receipt.Mode != tools.MessageModeSteer {
		t.Fatalf("receipt = %#v", receipt)
	}
	result, err = Call(context.Background(), socketPath, Request{
		Method: "session.inbox",
		Params: mustJSON(map[string]string{"namespace": "messages", "id": spawned.ID}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var inbox []tools.QueuedMessage
	if err := json.Unmarshal(result, &inbox); err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 1 || inbox[0].Content != "inspect the failing test" {
		t.Fatalf("inbox = %#v", inbox)
	}
	runner.release <- struct{}{}
	if _, err := manager.Kill(spawned.ID); err != nil {
		t.Fatal(err)
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
func TestManagerCompactIsAsyncAndCoalesced(t *testing.T) {
	runner := &controlledRunner{
		started:  make(chan struct{}),
		received: make(chan agent.ControlRequest, 1),
		release:  make(chan struct{}),
	}
	manager, err := NewManager(func(string) (Runner, string, error) {
		return runner, "test-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	spawned, err := manager.SpawnInNamespace("test", "", "compact")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}

	first, err := manager.CompactInNamespace("test", spawned.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "queued" || first.CommandID == "" {
		t.Fatalf("first compact = %#v", first)
	}
	second, err := manager.CompactInNamespace("test", spawned.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.CommandID != first.CommandID || second.Status != "queued" {
		t.Fatalf("coalesced compact = %#v, first = %#v", second, first)
	}
	select {
	case <-runner.received:
	case <-time.After(time.Second):
		t.Fatal("compact request was not delivered")
	}

	waitResult := make(chan CompactResult, 1)
	go func() {
		result, waitErr := manager.CompactInNamespace("test", spawned.ID, true)
		if waitErr != nil {
			t.Errorf("wait compact: %v", waitErr)
			return
		}
		waitResult <- result
	}()
	close(runner.release)
	select {
	case result := <-waitResult:
		if result.Status != "no_change" {
			t.Fatalf("wait compact = %#v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("wait compact did not finish")
	}
}
func TestServerSessionCompact(t *testing.T) {
	runner := &controlledRunner{
		started:  make(chan struct{}),
		received: make(chan agent.ControlRequest, 1),
		release:  make(chan struct{}),
	}
	manager, err := NewManager(func(string) (Runner, string, error) {
		return runner, "test-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)
	socketPath := testSocketPath(t)
	listener, err := ListenUnix(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()

	spawned, err := manager.SpawnInNamespace("test", "", "compact")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	result, err := Call(context.Background(), socketPath, Request{
		Method: "session.compact",
		Params: mustJSON(map[string]any{
			"namespace": "test",
			"id":        spawned.ID,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	var compact CompactResult
	if err := json.Unmarshal(result, &compact); err != nil {
		t.Fatal(err)
	}
	if compact.Status != "queued" || compact.SessionID != spawned.ID {
		t.Fatalf("compact = %#v", compact)
	}
	close(runner.release)
}

func TestCallReturnsDaemonError(t *testing.T) {
	factory := func(string) (Runner, string, error) { return testRunner{}, "test-model", nil }
	manager, err := NewManager(factory)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)
	socketPath := testSocketPath(t)
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
		if inspected.Status == "idle" {
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
	if recovered.Status != "idle" || recovered.Final != "done" {
		t.Fatalf("recovered = %#v", recovered)
	}
}
func TestManagerPersistsContextCompactionEvent(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(func(string) (Runner, string, error) {
		return contextEventRunner{}, "test-model", nil
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	spawned, err := manager.SpawnInNamespace("compaction", "", "persist compaction")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		inspected, inspectErr := manager.InspectInNamespace("compaction", spawned.ID[:8])
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		if inspected.Status == "idle" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent did not complete: %#v", inspected)
		}
		time.Sleep(time.Millisecond)
	}
	path, err := store.SessionPath("compaction", spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"type":"context.compacted"`) || !strings.Contains(text, `"compaction_number":1`) {
		t.Fatalf("session log = %s", text)
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
	socketPath := testSocketPath(t)
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
	socketPath := testSocketPath(t)
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

func TestManagerShutdownCancelsActiveSessions(t *testing.T) {
	manager, err := NewManager(func(string) (Runner, string, error) {
		return testRunner{wait: true}, "test-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	spawned, err := manager.SpawnInNamespace("shutdown", "", "stop me")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	inspected, err := manager.Inspect(spawned.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Status != "canceled" {
		t.Fatalf("shutdown status = %#v", inspected)
	}
}

func TestServerDaemonLifecycleRPC(t *testing.T) {
	manager, err := NewManager(func(string) (Runner, string, error) {
		return testRunner{}, "test-model", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(manager)
	server.SetDaemonStatus(DaemonStatus{State: "running", PID: 42, SocketPath: "/tmp/agentsd.sock"})
	shutdown := make(chan struct{})
	server.SetShutdownHandler(func() {
		close(shutdown)
	})

	result, err := server.handleRequest(Request{Method: "daemon.status"})
	if err != nil {
		t.Fatal(err)
	}
	status, ok := result.(DaemonStatus)
	if !ok {
		t.Fatalf("status type = %T", result)
	}
	if status.State != "running" || status.PID != 42 {
		t.Fatalf("status = %#v", status)
	}
	if _, err := server.handleRequest(Request{Method: "daemon.shutdown"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Fatal("shutdown handler was not called")
	}
}
