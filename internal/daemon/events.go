package daemon

import (
	"sync"

	"agentsd/internal/session"

	"github.com/google/uuid"
)

const eventSubscriptionQueueCapacity = 256

type eventSubscription struct {
	ID        string
	Namespace string
	SessionID string
	events    chan session.Event
	done      chan struct{}
	closeOnce sync.Once
	reason    string
}

func newEventSubscription(namespace, sessionID string) *eventSubscription {
	return &eventSubscription{
		ID:        uuid.NewString(),
		Namespace: namespace,
		SessionID: sessionID,
		events:    make(chan session.Event, eventSubscriptionQueueCapacity),
		done:      make(chan struct{}),
	}
}

func (subscription *eventSubscription) enqueue(event session.Event) bool {
	select {
	case <-subscription.done:
		return false
	default:
	}
	select {
	case subscription.events <- event:
		return true
	default:
		subscription.close("slow_consumer")
		return false
	}
}

func (subscription *eventSubscription) close(reason string) {
	subscription.closeOnce.Do(func() {
		subscription.reason = reason
		close(subscription.done)
	})
}

func (subscription *eventSubscription) doneReason() string {
	select {
	case <-subscription.done:
		return subscription.reason
	default:
		return ""
	}
}

type eventHub struct {
	mu            sync.RWMutex
	subscriptions map[string]map[string]*eventSubscription
}

func newEventHub() *eventHub {
	return &eventHub{subscriptions: make(map[string]map[string]*eventSubscription)}
}

func eventSessionKey(namespace, sessionID string) string {
	return namespace + "\x00" + sessionID
}

func (hub *eventHub) subscribe(namespace, sessionID string) *eventSubscription {
	subscription := newEventSubscription(namespace, sessionID)
	key := eventSessionKey(namespace, sessionID)
	hub.mu.Lock()
	group := hub.subscriptions[key]
	if group == nil {
		group = make(map[string]*eventSubscription)
		hub.subscriptions[key] = group
	}
	group[subscription.ID] = subscription
	hub.mu.Unlock()
	return subscription
}

func (hub *eventHub) unsubscribe(subscription *eventSubscription, reason string) {
	if subscription == nil {
		return
	}
	key := eventSessionKey(subscription.Namespace, subscription.SessionID)
	hub.mu.Lock()
	if group := hub.subscriptions[key]; group != nil {
		delete(group, subscription.ID)
		if len(group) == 0 {
			delete(hub.subscriptions, key)
		}
	}
	hub.mu.Unlock()
	subscription.close(reason)
}

func (hub *eventHub) publish(namespace, sessionID string, event session.Event) {
	key := eventSessionKey(namespace, sessionID)
	hub.mu.RLock()
	group := hub.subscriptions[key]
	subscriptions := make([]*eventSubscription, 0, len(group))
	for _, subscription := range group {
		subscriptions = append(subscriptions, subscription)
	}
	hub.mu.RUnlock()
	for _, subscription := range subscriptions {
		if subscription.enqueue(event) {
			continue
		}
		hub.unsubscribe(subscription, subscription.doneReason())
	}
}

func (hub *eventHub) closeSession(namespace, sessionID, reason string) {
	key := eventSessionKey(namespace, sessionID)
	hub.mu.Lock()
	group := hub.subscriptions[key]
	delete(hub.subscriptions, key)
	hub.mu.Unlock()
	for _, subscription := range group {
		subscription.close(reason)
	}
}
