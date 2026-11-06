package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"agentsd/internal/session"
	"agentsd/internal/tools"

	"github.com/google/uuid"
)

type mailboxEventSink func(eventType string, message tools.QueuedMessage) error

type mailboxWaitResult struct {
	message tools.QueuedMessage
	err     error
}

type mailboxWaiter struct {
	filter tools.MessageFilter
	done   chan mailboxWaitResult
}

type messageMailbox struct {
	mu       sync.Mutex
	steering []tools.QueuedMessage
	followUp []tools.QueuedMessage
	waiters  []*mailboxWaiter
	activity chan struct{}
	onEvent  mailboxEventSink
}

func newMessageMailbox(onEvent mailboxEventSink) *messageMailbox {
	return &messageMailbox{
		activity: make(chan struct{}),
		onEvent:  onEvent,
	}
}

func (mailbox *messageMailbox) Enqueue(message tools.QueuedMessage) (string, error) {
	if message.ID == "" {
		message.ID = uuid.NewString()
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	if message.Mode != tools.MessageModeSteer && message.Mode != tools.MessageModeFollowUp {
		return "", fmt.Errorf("daemon: unsupported message mode %q", message.Mode)
	}
	if message.Content == "" {
		return "", fmt.Errorf("daemon: message content is required")
	}

	mailbox.mu.Lock()
	waiterIndex := mailbox.matchingWaiterLocked(message)
	var waiter *mailboxWaiter
	if waiterIndex >= 0 {
		waiter = mailbox.waiters[waiterIndex]
		mailbox.waiters = append(mailbox.waiters[:waiterIndex], mailbox.waiters[waiterIndex+1:]...)
	} else {
		if message.Mode == tools.MessageModeSteer {
			mailbox.steering = append(mailbox.steering, message)
		} else {
			mailbox.followUp = append(mailbox.followUp, message)
		}
		mailbox.signalLocked()
	}
	mailbox.mu.Unlock()

	eventType := session.EventMessageQueued
	status := "queued"
	if waiter != nil {
		eventType = session.EventMessageWaitConsumed
		status = "waiting"
	}
	if err := mailbox.emit(eventType, message); err != nil {
		mailbox.restore(message)
		if waiter != nil {
			waiter.done <- mailboxWaitResult{err: err}
		}
		return "", err
	}
	if waiter != nil {
		waiter.done <- mailboxWaitResult{message: message}
	}
	return status, nil
}

func (mailbox *messageMailbox) Take(mode tools.MessageMode, limit int) ([]tools.QueuedMessage, error) {
	if mode != tools.MessageModeSteer && mode != tools.MessageModeFollowUp {
		return nil, fmt.Errorf("daemon: unsupported message mode %q", mode)
	}
	if limit <= 0 {
		return nil, nil
	}
	mailbox.mu.Lock()
	messages := mailbox.takeLocked(mode, limit)
	mailbox.mu.Unlock()
	if len(messages) == 0 {
		return nil, nil
	}
	for index, message := range messages {
		if err := mailbox.emit(session.EventMessageDelivered, message); err != nil {
			mailbox.restoreFront(messages[index:])
			return nil, err
		}
	}
	return messages, nil
}

func (mailbox *messageMailbox) Wait(
	ctx context.Context,
	filter tools.MessageFilter,
	timeout time.Duration,
) (tools.QueuedMessage, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return tools.QueuedMessage{}, false, err
	}
	mailbox.mu.Lock()
	if message, ok := mailbox.takeMatchingLocked(filter); ok {
		mailbox.mu.Unlock()
		if err := mailbox.emit(session.EventMessageWaitConsumed, message); err != nil {
			mailbox.restore(message)
			return tools.QueuedMessage{}, false, err
		}
		return message, true, nil
	}
	waiter := &mailboxWaiter{filter: filter, done: make(chan mailboxWaitResult, 1)}
	mailbox.waiters = append(mailbox.waiters, waiter)
	mailbox.mu.Unlock()

	var timer *time.Timer
	var timerC <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		timerC = timer.C
		defer timer.Stop()
	}
	select {
	case result := <-waiter.done:
		return result.message, result.message.ID != "" && result.err == nil, result.err
	case <-timerC:
		if mailbox.removeWaiter(waiter) {
			return tools.QueuedMessage{}, false, nil
		}
		result := <-waiter.done
		return result.message, result.message.ID != "" && result.err == nil, result.err
	case <-ctx.Done():
		if mailbox.removeWaiter(waiter) {
			return tools.QueuedMessage{}, false, ctx.Err()
		}
		result := <-waiter.done
		return result.message, result.message.ID != "" && result.err == nil, result.err
	}
}

func (mailbox *messageMailbox) Has(mode tools.MessageMode) bool {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	switch mode {
	case tools.MessageModeSteer:
		return len(mailbox.steering) > 0
	case tools.MessageModeFollowUp:
		return len(mailbox.followUp) > 0
	default:
		return false
	}
}

func (mailbox *messageMailbox) HasAny() bool {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	return len(mailbox.steering) > 0 || len(mailbox.followUp) > 0
}

func (mailbox *messageMailbox) TakeNext() ([]tools.QueuedMessage, error) {
	if mailbox.Has(tools.MessageModeSteer) {
		return mailbox.Take(tools.MessageModeSteer, 1)
	}
	return mailbox.Take(tools.MessageModeFollowUp, 1)
}

func (mailbox *messageMailbox) TakeFollowUpIfNoSteering() ([]tools.QueuedMessage, error) {
	mailbox.mu.Lock()
	if len(mailbox.steering) > 0 || len(mailbox.followUp) == 0 {
		mailbox.mu.Unlock()
		return nil, nil
	}
	message := mailbox.followUp[0]
	mailbox.followUp = mailbox.followUp[1:]
	mailbox.mu.Unlock()
	if err := mailbox.emit(session.EventMessageDelivered, message); err != nil {
		mailbox.restoreFront([]tools.QueuedMessage{message})
		return nil, err
	}
	return []tools.QueuedMessage{message}, nil
}

func (mailbox *messageMailbox) Snapshot() []tools.QueuedMessage {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	messages := make([]tools.QueuedMessage, 0, len(mailbox.steering)+len(mailbox.followUp))
	messages = append(messages, mailbox.steering...)
	messages = append(messages, mailbox.followUp...)
	return messages
}

func (mailbox *messageMailbox) Len() int {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	return len(mailbox.steering) + len(mailbox.followUp)
}

func (mailbox *messageMailbox) Activity() <-chan struct{} {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	return mailbox.activity
}

func (mailbox *messageMailbox) takeLocked(mode tools.MessageMode, limit int) []tools.QueuedMessage {
	if mode == tools.MessageModeSteer {
		limit = minInt(limit, len(mailbox.steering))
		messages := append([]tools.QueuedMessage(nil), mailbox.steering[:limit]...)
		mailbox.steering = mailbox.steering[limit:]
		return messages
	}
	limit = minInt(limit, len(mailbox.followUp))
	messages := append([]tools.QueuedMessage(nil), mailbox.followUp[:limit]...)
	mailbox.followUp = mailbox.followUp[limit:]
	return messages
}

func (mailbox *messageMailbox) takeMatchingLocked(filter tools.MessageFilter) (tools.QueuedMessage, bool) {
	if message, ok := takeMatchingFromSlice(&mailbox.steering, filter); ok {
		return message, true
	}
	return takeMatchingFromSlice(&mailbox.followUp, filter)
}

func takeMatchingFromSlice(messages *[]tools.QueuedMessage, filter tools.MessageFilter) (tools.QueuedMessage, bool) {
	for index, message := range *messages {
		if filter.From != "" && message.From != filter.From {
			continue
		}
		*messages = append((*messages)[:index], (*messages)[index+1:]...)
		return message, true
	}
	return tools.QueuedMessage{}, false
}

func (mailbox *messageMailbox) matchingWaiterLocked(message tools.QueuedMessage) int {
	for index, waiter := range mailbox.waiters {
		if waiter.filter.From == "" || waiter.filter.From == message.From {
			return index
		}
	}
	return -1
}

func (mailbox *messageMailbox) removeWaiter(target *mailboxWaiter) bool {
	mailbox.mu.Lock()
	defer mailbox.mu.Unlock()
	for index, waiter := range mailbox.waiters {
		if waiter != target {
			continue
		}
		mailbox.waiters = append(mailbox.waiters[:index], mailbox.waiters[index+1:]...)
		return true
	}
	return false
}

func (mailbox *messageMailbox) restore(message tools.QueuedMessage) {
	mailbox.restoreFront([]tools.QueuedMessage{message})
}

func (mailbox *messageMailbox) restoreFront(messages []tools.QueuedMessage) {
	if len(messages) == 0 {
		return
	}
	mailbox.mu.Lock()
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Mode == tools.MessageModeSteer {
			mailbox.steering = append([]tools.QueuedMessage{message}, mailbox.steering...)
		} else {
			mailbox.followUp = append([]tools.QueuedMessage{message}, mailbox.followUp...)
		}
	}
	mailbox.signalLocked()
	mailbox.mu.Unlock()
}

func (mailbox *messageMailbox) signalLocked() {
	close(mailbox.activity)
	mailbox.activity = make(chan struct{})
}

func (mailbox *messageMailbox) emit(eventType string, message tools.QueuedMessage) error {
	if mailbox.onEvent == nil {
		return nil
	}
	return mailbox.onEvent(eventType, message)
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
