package daemon

import (
	"context"
	"testing"
	"time"

	"agentsd/internal/tools"
)

func TestMessageMailboxWaitConsumesDirectMessage(t *testing.T) {
	events := make(chan string, 1)
	mailbox := newMessageMailbox(func(eventType string, _ tools.QueuedMessage) error {
		events <- eventType
		return nil
	})
	result := make(chan struct {
		message  tools.QueuedMessage
		received bool
		err      error
	}, 1)
	go func() {
		message, received, err := mailbox.Wait(context.Background(), tools.MessageFilter{From: "operator"}, time.Second)
		result <- struct {
			message  tools.QueuedMessage
			received bool
			err      error
		}{message: message, received: received, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		mailbox.mu.Lock()
		waiterRegistered := len(mailbox.waiters) == 1
		mailbox.mu.Unlock()
		if waiterRegistered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("mailbox waiter was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	status, err := mailbox.Enqueue(tools.QueuedMessage{
		ID:      "message-1",
		Mode:    tools.MessageModeSteer,
		Content: "continue",
		From:    "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if status != "waiting" {
		t.Fatalf("status = %q", status)
	}
	outcome := <-result
	if outcome.err != nil || !outcome.received || outcome.message.Content != "continue" {
		t.Fatalf("outcome = %#v", outcome)
	}
	if event := <-events; event != "message.wait_consumed" {
		t.Fatalf("event = %q", event)
	}
	if got := mailbox.Snapshot(); len(got) != 0 {
		t.Fatalf("mailbox = %#v", got)
	}
}
