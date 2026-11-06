package daemon

import (
	"context"
	"testing"
	"time"

	"agentsd/internal/session"
)

func TestWebSocketBroadcastsSessionEventsToMultipleListeners(t *testing.T) {
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
	summary, err := manager.SpawnInNamespace("events", "", "stream events")
	if err != nil {
		t.Fatal(err)
	}
	waitForAgentStatus(t, manager, "events", summary.ID, "idle")
	history, err := manager.EventsInNamespace("events", summary.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 {
		t.Fatal("session history is empty")
	}

	wsPath := WebSocketSocketPath(testSocketPath(t))
	listener, err := ListenUnix(wsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	server := NewServer(manager)
	go func() { _ = server.ServeWebSocket(listener) }()

	first, _, err := DialWebSocket(context.Background(), wsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, _, err := DialWebSocket(context.Background(), wsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	firstSubscription := subscribeToSession(t, first, "events", summary.ID, history[len(history)-1].ID)
	secondSubscription := subscribeToSession(t, second, "events", summary.ID, history[len(history)-1].ID)
	if firstSubscription.SubscriptionID == secondSubscription.SubscriptionID {
		t.Fatalf("subscription IDs are equal: %q", firstSubscription.SubscriptionID)
	}

	manager.mu.RLock()
	record := manager.agents[summary.ID]
	manager.mu.RUnlock()
	if record == nil {
		t.Fatalf("session %q not found", summary.ID)
	}
	if err := manager.appendEvent(record, session.NewEvent("test.event", map[string]any{"value": "shared"})); err != nil {
		t.Fatal(err)
	}

	firstEvent := readEventFrame(t, first)
	secondEvent := readEventFrame(t, second)
	if firstEvent.Type != "event" || secondEvent.Type != "event" {
		t.Fatalf("frames = %#v, %#v", firstEvent, secondEvent)
	}
	if firstEvent.SubscriptionID != firstSubscription.SubscriptionID || secondEvent.SubscriptionID != secondSubscription.SubscriptionID {
		t.Fatalf("subscription IDs = %q, %q", firstEvent.SubscriptionID, secondEvent.SubscriptionID)
	}
	if firstEvent.Event == nil || secondEvent.Event == nil {
		t.Fatalf("event frames = %#v, %#v", firstEvent, secondEvent)
	}
	if firstEvent.Event.ID != secondEvent.Event.ID || firstEvent.Event.Type != "test.event" || secondEvent.Event.Type != "test.event" {
		t.Fatalf("events = %#v, %#v", firstEvent.Event, secondEvent.Event)
	}
}

func subscribeToSession(t *testing.T, connection interface {
	WriteJSON(any) error
	ReadJSON(any) error
}, namespace, sessionID, after string) eventFrame {
	t.Helper()
	if err := connection.WriteJSON(eventRequest{
		Type:      "subscribe",
		ID:        "subscribe-request",
		Namespace: namespace,
		SessionID: sessionID,
		After:     after,
	}); err != nil {
		t.Fatal(err)
	}
	var frame eventFrame
	if err := connection.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != "subscribed" {
		t.Fatalf("subscribe frame = %#v", frame)
	}
	return frame
}

func readEventFrame(t *testing.T, connection interface {
	SetReadDeadline(time.Time) error
	ReadJSON(any) error
}) eventFrame {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var frame eventFrame
	if err := connection.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	return frame
}

func waitForAgentStatus(t *testing.T, manager *Manager, namespace, id, status string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		summary, err := manager.InspectInNamespace(namespace, id)
		if err == nil && summary.Status == status {
			return
		}
		time.Sleep(time.Millisecond)
	}
	summary, err := manager.InspectInNamespace(namespace, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("status = %q, want %q", summary.Status, status)
}
