package daemon

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"agentsd/internal/session"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	webSocketPath       = "/events"
	webSocketWriteWait  = 10 * time.Second
	webSocketPongWait   = 60 * time.Second
	webSocketPingPeriod = (webSocketPongWait * 9) / 10
	webSocketReadLimit  = 1 << 20
)

type eventRequest struct {
	Type           string `json:"type"`
	ID             string `json:"id,omitempty"`
	SubscriptionID string `json:"subscription_id,omitempty"`
	Namespace      string `json:"namespace,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	After          string `json:"after,omitempty"`
}

type eventFrame struct {
	Type           string         `json:"type"`
	ID             string         `json:"id,omitempty"`
	SubscriptionID string         `json:"subscription_id,omitempty"`
	Namespace      string         `json:"namespace,omitempty"`
	SessionID      string         `json:"session_id,omitempty"`
	Event          *session.Event `json:"event,omitempty"`
	Error          string         `json:"error,omitempty"`
}

type eventClient struct {
	manager         *Manager
	connection      *websocket.Conn
	outbound        chan eventFrame
	done            chan struct{}
	closeOnce       sync.Once
	subscriptionsMu sync.Mutex
	subscriptions   map[string]*eventSubscription
}

func (server *Server) ServeWebSocket(listener net.Listener) error {
	httpServer := &http.Server{Handler: http.HandlerFunc(server.handleWebSocket)}
	return httpServer.Serve(listener)
}

func (server *Server) handleWebSocket(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path != webSocketPath {
		http.NotFound(writer, request)
		return
	}
	connection, err := (&websocket.Upgrader{
		ReadBufferSize:  4096,
		WriteBufferSize: 4096,
		CheckOrigin:     func(*http.Request) bool { return true },
	}).Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	client := newEventClient(server.manager, connection)
	client.run()
}

func newEventClient(manager *Manager, connection *websocket.Conn) *eventClient {
	return &eventClient{
		manager:       manager,
		connection:    connection,
		outbound:      make(chan eventFrame, eventSubscriptionQueueCapacity),
		done:          make(chan struct{}),
		subscriptions: make(map[string]*eventSubscription),
	}
}

func (client *eventClient) run() {
	go client.writeLoop()
	client.readLoop()
	client.close()
}

func (client *eventClient) readLoop() {
	client.connection.SetReadLimit(webSocketReadLimit)
	_ = client.connection.SetReadDeadline(time.Now().Add(webSocketPongWait))
	client.connection.SetPongHandler(func(string) error {
		return client.connection.SetReadDeadline(time.Now().Add(webSocketPongWait))
	})
	for {
		var request eventRequest
		if err := client.connection.ReadJSON(&request); err != nil {
			return
		}
		client.handleRequest(request)
	}
}

func (client *eventClient) handleRequest(request eventRequest) {
	switch strings.ToLower(strings.TrimSpace(request.Type)) {
	case "subscribe":
		client.subscribe(request)
	case "unsubscribe":
		client.unsubscribe(request)
	case "ping":
		client.queue(eventFrame{Type: "pong", ID: request.ID})
	default:
		client.queue(eventFrame{Type: "error", ID: request.ID, Error: "unsupported event request"})
	}
}

func (client *eventClient) subscribe(request eventRequest) {
	if strings.TrimSpace(request.ID) == "" {
		request.ID = newRequestID()
	}
	if strings.TrimSpace(request.SessionID) == "" {
		client.queue(eventFrame{Type: "error", ID: request.ID, Error: "session_id is required"})
		return
	}
	subscription, history, err := client.manager.SubscribeInNamespace(request.Namespace, request.SessionID, request.After)
	if err != nil {
		client.queue(eventFrame{Type: "error", ID: request.ID, Error: err.Error()})
		return
	}
	client.subscriptionsMu.Lock()
	client.subscriptions[subscription.ID] = subscription
	client.subscriptionsMu.Unlock()

	if !client.queue(eventFrame{
		Type:           "subscribed",
		ID:             request.ID,
		SubscriptionID: subscription.ID,
		Namespace:      subscription.Namespace,
		SessionID:      subscription.SessionID,
	}) {
		client.manager.events.unsubscribe(subscription, "connection_closed")
		return
	}
	for index := range history {
		if !client.queue(client.eventFrame(subscription, history[index])) {
			client.manager.events.unsubscribe(subscription, "connection_closed")
			return
		}
	}
	go client.forward(subscription)
}

func (client *eventClient) unsubscribe(request eventRequest) {
	subscriptionID := strings.TrimSpace(request.SubscriptionID)
	if subscriptionID == "" {
		subscriptionID = strings.TrimSpace(request.ID)
	}
	subscription := client.detach(subscriptionID)
	if subscription == nil {
		client.queue(eventFrame{Type: "error", ID: request.ID, Error: "subscription not found"})
		return
	}
	client.manager.events.unsubscribe(subscription, "unsubscribed")
	client.queue(eventFrame{Type: "unsubscribed", ID: request.ID, SubscriptionID: subscription.ID})
}

func (client *eventClient) forward(subscription *eventSubscription) {
	for {
		select {
		case <-client.done:
			return
		default:
		}
		select {
		case event := <-subscription.events:
			if !client.queue(client.eventFrame(subscription, event)) {
				client.manager.events.unsubscribe(subscription, "connection_closed")
				return
			}
		case <-subscription.done:
			client.detach(subscription.ID)
			if reason := subscription.doneReason(); reason == "slow_consumer" {
				client.queue(eventFrame{
					Type:           "subscription_closed",
					SubscriptionID: subscription.ID,
					Namespace:      subscription.Namespace,
					SessionID:      subscription.SessionID,
					Error:          reason,
				})
			}
			return
		}
	}
}

func (client *eventClient) eventFrame(subscription *eventSubscription, event session.Event) eventFrame {
	return eventFrame{
		Type:           "event",
		SubscriptionID: subscription.ID,
		Namespace:      subscription.Namespace,
		SessionID:      subscription.SessionID,
		Event:          &event,
	}
}

func (client *eventClient) queue(frame eventFrame) bool {
	select {
	case <-client.done:
		return false
	case client.outbound <- frame:
		return true
	}
}

func (client *eventClient) detach(subscriptionID string) *eventSubscription {
	client.subscriptionsMu.Lock()
	defer client.subscriptionsMu.Unlock()
	subscription := client.subscriptions[subscriptionID]
	delete(client.subscriptions, subscriptionID)
	return subscription
}

func (client *eventClient) close() {
	client.closeOnce.Do(func() {
		close(client.done)
		client.subscriptionsMu.Lock()
		subscriptions := make([]*eventSubscription, 0, len(client.subscriptions))
		for _, subscription := range client.subscriptions {
			subscriptions = append(subscriptions, subscription)
		}
		client.subscriptions = make(map[string]*eventSubscription)
		client.subscriptionsMu.Unlock()
		for _, subscription := range subscriptions {
			client.manager.events.unsubscribe(subscription, "connection_closed")
		}
		_ = client.connection.Close()
	})
}

func (client *eventClient) writeLoop() {
	ticker := time.NewTicker(webSocketPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-client.done:
			return
		case frame := <-client.outbound:
			if err := client.connection.SetWriteDeadline(time.Now().Add(webSocketWriteWait)); err != nil {
				client.close()
				return
			}
			if err := client.connection.WriteJSON(frame); err != nil {
				client.close()
				return
			}
		case <-ticker.C:
			if err := client.connection.SetWriteDeadline(time.Now().Add(webSocketWriteWait)); err != nil {
				client.close()
				return
			}
			if err := client.connection.WriteMessage(websocket.PingMessage, nil); err != nil {
				client.close()
				return
			}
		}
	}
}

func DialWebSocket(ctx context.Context, socketPath string) (*websocket.Conn, *http.Response, error) {
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return dialer.DialContext(ctx, "ws://agentsd/events", nil)
}

func newRequestID() string {
	return uuid.NewString()
}
