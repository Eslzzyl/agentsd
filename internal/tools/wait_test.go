package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type waitTestQueue struct {
	messages chan QueuedMessage
}

func (queue *waitTestQueue) Take(MessageMode, int) ([]QueuedMessage, error) {
	return nil, nil
}

func (queue *waitTestQueue) Wait(ctx context.Context, _ MessageFilter, timeout time.Duration) (QueuedMessage, bool, error) {
	var timer *time.Timer
	var timerC <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timerC = timer.C
	}
	select {
	case message := <-queue.messages:
		return message, true, nil
	case <-timerC:
		return QueuedMessage{}, false, nil
	case <-ctx.Done():
		return QueuedMessage{}, false, ctx.Err()
	}
}

func TestWaitToolReturnsMailboxMessage(t *testing.T) {
	queue := &waitTestQueue{messages: make(chan QueuedMessage, 1)}
	states := make(chan bool, 2)
	go func() {
		time.Sleep(10 * time.Millisecond)
		queue.messages <- QueuedMessage{ID: "message-1", Mode: MessageModeSteer, Content: "continue", From: "operator"}
	}()

	result, err := (WaitTool{}).Execute(context.Background(), ExecutionContext{
		Session: SessionContext{
			MessageQueue: queue,
			OnWaiting:    func(waiting bool) { states <- waiting },
		},
	}, waitArguments{})
	if err != nil {
		t.Fatal(err)
	}
	var response waitResponse
	if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || response.TimedOut || response.Message == nil || response.Message.Content != "continue" {
		t.Fatalf("response = %#v", response)
	}
	if got := <-states; !got {
		t.Fatal("wait did not report entering the waiting state")
	}
	if got := <-states; got {
		t.Fatal("wait did not report leaving the waiting state")
	}
}

func TestWaitToolTimesOut(t *testing.T) {
	queue := &waitTestQueue{messages: make(chan QueuedMessage)}
	result, err := (WaitTool{}).Execute(context.Background(), ExecutionContext{
		Session: SessionContext{MessageQueue: queue},
	}, waitArguments{TimeoutMS: 10})
	if err != nil {
		t.Fatal(err)
	}
	var response waitResponse
	if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || !response.TimedOut || response.Message != nil {
		t.Fatalf("response = %#v", response)
	}
}
