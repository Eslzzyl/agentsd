package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"agentsd/internal/agent"
	"agentsd/internal/llm"
	"agentsd/internal/session"
	"agentsd/internal/tools"

	"github.com/google/uuid"
)

type Runner interface {
	Run(context.Context, agent.RunRequest) (agent.RunResult, error)
}

type RuntimeFactory func(model string) (Runner, string, error)

type queueAwareRunner interface {
	UsesMessageQueue() bool
}

type compactRunner interface {
	Compact(context.Context, agent.RunRequest) (agent.RunResult, agent.ControlResult, error)
}

type AgentSummary struct {
	ID             string     `json:"id"`
	Namespace      string     `json:"namespace"`
	Model          string     `json:"model"`
	Task           string     `json:"task"`
	Workspace      string     `json:"workspace"`
	Status         string     `json:"status"`
	Turns          int        `json:"turns"`
	QueuedMessages int        `json:"queued_messages,omitempty"`
	Final          string     `json:"final,omitempty"`
	Error          string     `json:"error,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type MessageReceipt struct {
	MessageID string            `json:"message_id"`
	SessionID string            `json:"session_id"`
	Mode      tools.MessageMode `json:"mode"`
	Status    string            `json:"status"`
}

type messageSendOptions struct {
	Source string
	From   string
}
type CompactResult struct {
	CommandID        string `json:"command_id"`
	SessionID        string `json:"session_id"`
	Status           string `json:"status"`
	CompactionNumber int    `json:"compaction_number,omitempty"`
	TokensBefore     int    `json:"tokens_before,omitempty"`
	RemovedMessages  int    `json:"removed_messages,omitempty"`
	KeptMessages     int    `json:"kept_messages,omitempty"`
	Error            string `json:"error,omitempty"`
}

type compactionCommand struct {
	mu       sync.Mutex
	result   CompactResult
	done     chan struct{}
	finished bool
}

func newCompactionCommand(sessionID string) *compactionCommand {
	return &compactionCommand{
		result: CompactResult{
			CommandID: uuid.NewString(),
			SessionID: sessionID,
			Status:    "queued",
		},
		done: make(chan struct{}),
	}
}

func (command *compactionCommand) complete(result agent.ControlResult) {
	command.mu.Lock()
	defer command.mu.Unlock()
	if command.finished {
		return
	}
	command.result.Status = string(result.Status)
	if result.Compaction != nil {
		command.result.CompactionNumber = result.Compaction.CompactionNumber
		command.result.TokensBefore = result.Compaction.TokensBefore
		command.result.RemovedMessages = result.Compaction.RemovedMessages
		command.result.KeptMessages = result.Compaction.KeptMessages
	}
	command.result.Error = result.Error
	command.finished = true
	close(command.done)
}

func (command *compactionCommand) fail(message string) {
	command.complete(agent.ControlResult{
		Status: agent.ControlFailed,
		Error:  message,
	})
}

func (command *compactionCommand) wait() {
	<-command.done
}

func (command *compactionCommand) snapshot() CompactResult {
	command.mu.Lock()
	defer command.mu.Unlock()
	return command.result
}

type managedAgent struct {
	summary         AgentSummary
	cancel          context.CancelFunc
	control         chan agent.ControlRequest
	mailbox         *messageMailbox
	messages        []llm.Message
	compaction      *compactionCommand
	lastCompaction  *compactionCommand
	cancelRequested bool
	persistenceErr  error
	deleted         bool
}

type Manager struct {
	mu      sync.RWMutex
	agents  map[string]*managedAgent
	factory RuntimeFactory
	store   *session.Store
}

type NamespaceResult struct {
	Name string `json:"name"`
}

type NamespaceRenameResult struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func NewManager(factory RuntimeFactory, stores ...*session.Store) (*Manager, error) {
	if factory == nil {
		return nil, fmt.Errorf("daemon: runtime factory is required")
	}
	if len(stores) > 1 {
		return nil, fmt.Errorf("daemon: only one session store is supported")
	}
	var store *session.Store
	if len(stores) == 1 {
		store = stores[0]
	}
	manager := &Manager{
		agents:  make(map[string]*managedAgent),
		factory: factory,
		store:   store,
	}
	if store == nil {
		return manager, nil
	}
	recovered, err := store.Recover()
	if err != nil {
		return nil, err
	}
	for _, item := range recovered {
		summary := summaryFromSession(item)
		manager.agents[summary.ID] = &managedAgent{summary: summary}
	}
	return manager, nil
}

func (manager *Manager) CreateNamespace(namespace string) error {
	if manager.store == nil {
		return fmt.Errorf("daemon: session store is required")
	}
	return manager.store.CreateNamespace(namespace)
}

func (manager *Manager) DeleteNamespace(namespace string, force ...bool) error {
	if manager.store == nil {
		return fmt.Errorf("daemon: session store is required")
	}
	if len(force) > 1 {
		return fmt.Errorf("daemon: delete namespace accepts at most one force flag")
	}
	namespace, err := session.NormalizeNamespace(namespace)
	if err != nil {
		return err
	}
	forceDelete := len(force) == 1 && force[0]
	if !forceDelete {
		return manager.store.DeleteNamespace(namespace)
	}

	manager.mu.Lock()
	affected := make(map[string]*managedAgent)
	for id, record := range manager.agents {
		if record.summary.Namespace != namespace {
			continue
		}
		record.deleted = true
		record.cancelRequested = true
		if record.cancel != nil {
			record.cancel()
		}
		affected[id] = record
	}
	manager.mu.Unlock()

	err = manager.store.DeleteNamespace(namespace, true)
	manager.mu.Lock()
	for id, record := range affected {
		if err == nil {
			delete(manager.agents, id)
			continue
		}
		record.deleted = false
	}
	manager.mu.Unlock()
	return err
}

func (manager *Manager) RenameNamespace(from, to string) error {
	if manager.store == nil {
		return fmt.Errorf("daemon: session store is required")
	}
	if err := manager.store.RenameNamespace(from, to); err != nil {
		return err
	}
	from, err := session.NormalizeNamespace(from)
	if err != nil {
		return err
	}
	to, err = session.NormalizeNamespace(to)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	for _, record := range manager.agents {
		if record.summary.Namespace == from {
			record.summary.Namespace = to
		}
	}
	manager.mu.Unlock()
	return nil
}

func (manager *Manager) ListNamespaces() ([]string, error) {
	if manager.store == nil {
		return nil, fmt.Errorf("daemon: session store is required")
	}
	return manager.store.ListNamespaces()
}

func (manager *Manager) Spawn(model, task string) (AgentSummary, error) {
	return AgentSummary{}, fmt.Errorf("daemon: namespace is required")
}

func (manager *Manager) SpawnInNamespace(namespace, model, task string) (AgentSummary, error) {
	if strings.TrimSpace(task) == "" {
		return AgentSummary{}, fmt.Errorf("daemon: task is required")
	}
	namespace, err := session.NormalizeNamespace(namespace)
	if err != nil {
		return AgentSummary{}, err
	}
	runner, resolvedModel, err := manager.factory(model)
	if err != nil {
		return AgentSummary{}, err
	}
	if runner == nil {
		return AgentSummary{}, fmt.Errorf("daemon: runtime factory returned nil runner")
	}
	if resolvedModel == "" {
		resolvedModel = model
	}
	workspace, err := os.Getwd()
	if err != nil {
		return AgentSummary{}, fmt.Errorf("daemon: determine working directory: %w", err)
	}
	startedAt := time.Now().UTC()
	id := uuid.NewString()
	if manager.store != nil {
		created, err := manager.store.Create(session.Header{
			ID:        id,
			Namespace: namespace,
			Model:     resolvedModel,
			Task:      task,
			Workspace: workspace,
			StartedAt: startedAt,
		})
		if err != nil {
			return AgentSummary{}, err
		}
		id = created.Header.ID
	}
	ctx, cancel := context.WithCancel(context.Background())
	record := &managedAgent{
		summary: AgentSummary{
			ID:        id,
			Namespace: namespace,
			Model:     resolvedModel,
			Task:      task,
			Workspace: workspace,
			Status:    "running",
			StartedAt: startedAt,
		},
		cancel:  cancel,
		control: make(chan agent.ControlRequest, 1),
		messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are a managed coding agent. Execute the task with the available tools and return a concise final report."},
			{Role: llm.RoleUser, Content: task},
		},
	}
	record.mailbox = newMessageMailbox(func(eventType string, message tools.QueuedMessage) error {
		err := manager.appendEvent(record, messageEventToSession(eventType, message))
		if err != nil {
			manager.setPersistenceError(record, err)
		}
		return err
	})
	manager.mu.Lock()
	manager.agents[id] = record
	summary := record.summary
	manager.mu.Unlock()
	if err := manager.appendEvent(record, session.NewEvent(session.EventAgentStarted, map[string]any{
		"model":     resolvedModel,
		"namespace": namespace,
		"task":      task,
		"workspace": workspace,
	})); err != nil {
		manager.mu.Lock()
		delete(manager.agents, id)
		manager.mu.Unlock()
		cancel()
		return AgentSummary{}, err
	}

	go manager.run(ctx, record, runner, resolvedModel)
	return summary, nil
}

func (manager *Manager) run(ctx context.Context, record *managedAgent, runner Runner, model string) {
	manager.mu.RLock()
	messages := cloneMessages(record.messages)
	manager.mu.RUnlock()
	queueAware := false
	if aware, ok := runner.(queueAwareRunner); ok {
		queueAware = aware.UsesMessageQueue()
	}

	for {
		if err := manager.prepareTurn(record, queueAware, &messages); err != nil {
			manager.finishRun(record, agent.RunResult{Messages: messages}, err)
			return
		}
		manager.markRunning(record)
		summary := manager.recordSummary(record)
		request := manager.runRequest(record, summary, model, messages)
		result, runErr := runner.Run(ctx, request)
		if len(result.Messages) > 0 {
			messages = cloneMessages(result.Messages)
		}
		manager.updateMessages(record, messages)
		if runErr != nil {
			manager.finishRun(record, result, runErr)
			return
		}

		manager.markIdle(record, result)
		for {
			if record.mailbox.HasAny() {
				break
			}
			select {
			case <-ctx.Done():
				manager.finishRun(record, agent.RunResult{}, ctx.Err())
				return
			case control := <-record.control:
				manager.handleIdleControl(ctx, record, runner, model, &messages, control)
			case <-record.mailbox.Activity():
			}
		}
	}
}

func (manager *Manager) prepareTurn(record *managedAgent, queueAware bool, messages *[]llm.Message) error {
	if record.mailbox == nil {
		return nil
	}
	var queued []tools.QueuedMessage
	var err error
	if queueAware {
		queued, err = record.mailbox.TakeFollowUpIfNoSteering()
	} else {
		queued, err = record.mailbox.TakeNext()
	}
	if err != nil {
		return err
	}
	for _, message := range queued {
		*messages = append(*messages, llm.Message{Role: llm.RoleUser, Content: message.Content})
	}
	return nil
}

func (manager *Manager) markRunning(record *managedAgent) {
	manager.mu.Lock()
	wasIdle := record.summary.Status == "idle"
	if wasIdle {
		record.lastCompaction = nil
	}
	record.summary.Status = "running"
	record.summary.FinishedAt = nil
	manager.mu.Unlock()
	if wasIdle {
		if err := manager.appendEvent(record, session.NewEvent(session.EventAgentResumed, nil)); err != nil {
			manager.setPersistenceError(record, err)
		}
	}
}

func (manager *Manager) markIdle(record *managedAgent, result agent.RunResult) {
	manager.mu.Lock()
	record.summary.Status = "idle"
	record.summary.Turns += result.Turns
	record.summary.Final = result.FinalMessage.Content
	record.summary.Error = ""
	summary := record.summary
	manager.mu.Unlock()
	if err := manager.appendEvent(record, session.NewEvent(session.EventAgentIdle, map[string]any{
		"turns": summary.Turns,
		"final": summary.Final,
	})); err != nil {
		manager.setPersistenceError(record, err)
	}
}

func (manager *Manager) finishRun(record *managedAgent, result agent.RunResult, runErr error) {
	finishedAt := time.Now().UTC()
	manager.mu.Lock()
	pendingCompaction := record.compaction
	record.compaction = nil
	record.summary.Turns += result.Turns
	record.summary.FinishedAt = &finishedAt
	cancelRequested := record.cancelRequested
	if runErr != nil {
		record.summary.Error = runErr.Error()
		if cancelRequested || errors.Is(runErr, context.Canceled) {
			record.summary.Status = "canceled"
		} else {
			record.summary.Status = "failed"
		}
	}
	finalSummary := record.summary
	persistenceErr := record.persistenceErr
	manager.mu.Unlock()
	if pendingCompaction != nil {
		manager.failCompaction(record, pendingCompaction, "agent ended before context compaction started")
	}

	eventType := session.EventAgentFailed
	if finalSummary.Status == "canceled" {
		eventType = session.EventAgentCanceled
	}
	fields := map[string]any{
		"finished_at": finishedAt,
		"turns":       finalSummary.Turns,
	}
	if finalSummary.Final != "" {
		fields["final"] = finalSummary.Final
	}
	if finalSummary.Error != "" {
		fields["error"] = finalSummary.Error
	}
	if persistenceErr != nil {
		fields["persistence_error"] = persistenceErr.Error()
	}
	if err := manager.appendEvent(record, session.NewEvent(eventType, fields)); err != nil {
		manager.setPersistenceError(record, err)
	}
}

func (manager *Manager) handleIdleControl(
	ctx context.Context,
	record *managedAgent,
	runner Runner,
	model string,
	messages *[]llm.Message,
	control agent.ControlRequest,
) {
	result := agent.ControlResult{Status: agent.ControlFailed}
	if control.Kind == agent.ControlContextCompact {
		compact, ok := runner.(compactRunner)
		if !ok {
			result.Error = "daemon: runner does not support idle context compaction"
		} else {
			runResult, compactResult, err := compact.Compact(ctx, manager.runRequest(record, manager.recordSummary(record), model, *messages))
			if len(runResult.Messages) > 0 {
				*messages = cloneMessages(runResult.Messages)
				manager.updateMessages(record, *messages)
			}
			result = compactResult
			if err != nil {
				result = agent.ControlResult{Status: agent.ControlFailed, Error: err.Error()}
			}
		}
	} else {
		result.Error = fmt.Sprintf("agent: unknown control %q", control.Kind)
	}
	if control.Complete != nil {
		control.Complete(result)
	}
}

func (manager *Manager) runRequest(record *managedAgent, summary AgentSummary, model string, messages []llm.Message) agent.RunRequest {
	return agent.RunRequest{
		Model:    model,
		Messages: cloneMessages(messages),
		Control:  record.control,
		Session: &tools.SessionContext{
			ID:            summary.ID,
			Namespace:     summary.Namespace,
			CWD:           summary.Workspace,
			WorkspaceRoot: summary.Workspace,
			MessageQueue:  record.mailbox,
			OnWaiting: func(waiting bool) {
				manager.updateWaiting(record, waiting)
			},
		},
		EventSink: func(event tools.Event) {
			if err := manager.appendEvent(record, eventToSession(event)); err != nil {
				manager.setPersistenceError(record, err)
			}
		},
		ContextEventSink: func(event agent.ContextEvent) {
			if err := manager.appendEvent(record, contextEventToSession(event)); err != nil {
				manager.setPersistenceError(record, err)
			}
		},
	}
}

func (manager *Manager) recordSummary(record *managedAgent) AgentSummary {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return record.summary
}

func (manager *Manager) updateMessages(record *managedAgent, messages []llm.Message) {
	manager.mu.Lock()
	record.messages = cloneMessages(messages)
	manager.mu.Unlock()
}

func (manager *Manager) updateWaiting(record *managedAgent, waiting bool) {
	manager.mu.Lock()
	if record.deleted {
		manager.mu.Unlock()
		return
	}
	if waiting {
		record.summary.Status = "waiting"
	} else if record.summary.Status == "waiting" {
		record.summary.Status = "running"
	}
	manager.mu.Unlock()
	eventType := session.EventSessionWoken
	if waiting {
		eventType = session.EventSessionWaiting
	}
	if err := manager.appendEvent(record, session.NewEvent(eventType, nil)); err != nil {
		manager.setPersistenceError(record, err)
	}
}

func (manager *Manager) setPersistenceError(record *managedAgent, err error) {
	manager.mu.Lock()
	record.persistenceErr = err
	manager.mu.Unlock()
}

func (manager *Manager) appendEvent(record *managedAgent, event session.Event) error {
	if manager.store == nil {
		return nil
	}
	manager.mu.RLock()
	if record.deleted {
		manager.mu.RUnlock()
		return nil
	}
	namespace := record.summary.Namespace
	id := record.summary.ID
	manager.mu.RUnlock()
	return manager.store.Append(namespace, id, event)
}

func eventToSession(event tools.Event) session.Event {
	timestamp := event.FinishedAt
	if timestamp.IsZero() {
		timestamp = event.StartedAt
	}
	data := map[string]any{
		"turn":       event.Turn,
		"call_id":    event.CallID,
		"tool_name":  event.ToolName,
		"started_at": event.StartedAt,
	}
	if !event.FinishedAt.IsZero() {
		data["finished_at"] = event.FinishedAt
	}
	if event.Result != nil {
		data["result"] = event.Result
	}
	if event.Error != "" {
		data["error"] = event.Error
	}
	return session.Event{Type: string(event.Type), Timestamp: timestamp, Data: data}
}

func contextEventToSession(event agent.ContextEvent) session.Event {
	return session.NewEvent(session.EventContextCompacted, map[string]any{
		"summary":           event.Summary,
		"tokens_before":     event.TokensBefore,
		"removed_messages":  event.RemovedMessages,
		"kept_messages":     event.KeptMessages,
		"compaction_number": event.CompactionNumber,
	})
}

func messageEventToSession(eventType string, message tools.QueuedMessage) session.Event {
	return session.NewEvent(eventType, map[string]any{
		"message_id": message.ID,
		"mode":       message.Mode,
		"content":    message.Content,
		"source":     message.Source,
		"from":       message.From,
		"created_at": message.CreatedAt,
	})
}

func cloneMessages(messages []llm.Message) []llm.Message {
	cloned := make([]llm.Message, len(messages))
	for index, message := range messages {
		cloned[index] = message
		cloned[index].Images = append([]llm.Image(nil), message.Images...)
		for imageIndex := range cloned[index].Images {
			cloned[index].Images[imageIndex].Data = append([]byte(nil), message.Images[imageIndex].Data...)
		}
		cloned[index].ToolCalls = append([]llm.ToolCall(nil), message.ToolCalls...)
	}
	return cloned
}

func (manager *Manager) List() []AgentSummary {
	return manager.listInNamespace("")
}

func (manager *Manager) ListInNamespace(namespace string) []AgentSummary {
	normalized, err := session.NormalizeNamespace(namespace)
	if err != nil {
		return nil
	}
	return manager.listInNamespace(normalized)
}

func (manager *Manager) ListAll() []AgentSummary {
	return manager.listInNamespace("")
}

func (manager *Manager) listInNamespace(namespace string) []AgentSummary {
	manager.mu.RLock()
	result := make([]AgentSummary, 0, len(manager.agents))
	for _, record := range manager.agents {
		if namespace != "" && record.summary.Namespace != namespace {
			continue
		}
		result = append(result, manager.summarySnapshotLocked(record))
	}
	manager.mu.RUnlock()
	sort.Slice(result, func(left, right int) bool {
		if result[left].StartedAt.Equal(result[right].StartedAt) {
			return result[left].ID < result[right].ID
		}
		return result[left].StartedAt.Before(result[right].StartedAt)
	})
	return result
}

func (manager *Manager) summarySnapshotLocked(record *managedAgent) AgentSummary {
	result := record.summary
	if record.mailbox != nil {
		result.QueuedMessages = record.mailbox.Len()
	}
	return result
}

func (manager *Manager) Inspect(id string) (AgentSummary, error) {
	return manager.inspectInNamespace("", id)
}

func (manager *Manager) InspectInNamespace(namespace, id string) (AgentSummary, error) {
	normalized, err := session.NormalizeNamespace(namespace)
	if err != nil {
		return AgentSummary{}, err
	}
	return manager.inspectInNamespace(normalized, id)
}

func (manager *Manager) inspectInNamespace(namespace, id string) (AgentSummary, error) {
	manager.mu.RLock()
	record, err := manager.findLocked(namespace, id)
	if err != nil {
		manager.mu.RUnlock()
		return AgentSummary{}, err
	}
	result := manager.summarySnapshotLocked(record)
	manager.mu.RUnlock()
	return result, nil
}
func (manager *Manager) Send(id, content string, mode tools.MessageMode) (MessageReceipt, error) {
	return manager.SendInNamespace("", id, content, mode)
}

func (manager *Manager) SendInNamespace(namespace, id, content string, mode tools.MessageMode) (MessageReceipt, error) {
	if strings.TrimSpace(namespace) == "" {
		return manager.sendInNamespace("", id, content, mode, messageSendOptions{Source: "operator", From: "operator"})
	}
	normalized, err := session.NormalizeNamespace(namespace)
	if err != nil {
		return MessageReceipt{}, err
	}
	return manager.sendInNamespace(normalized, id, content, mode, messageSendOptions{Source: "operator", From: "operator"})
}

func (manager *Manager) sendInNamespace(namespace, id, content string, mode tools.MessageMode, options messageSendOptions) (MessageReceipt, error) {
	if strings.TrimSpace(content) == "" {
		return MessageReceipt{}, fmt.Errorf("daemon: message content is required")
	}
	normalizedMode, err := normalizeMessageMode(mode)
	if err != nil {
		return MessageReceipt{}, err
	}
	manager.mu.RLock()
	record, findErr := manager.findLocked(namespace, id)
	if findErr != nil {
		manager.mu.RUnlock()
		return MessageReceipt{}, findErr
	}
	status := record.summary.Status
	mailbox := record.mailbox
	manager.mu.RUnlock()
	if status != "running" && status != "idle" && status != "waiting" {
		return MessageReceipt{}, fmt.Errorf("daemon: agent %q is %s; message delivery requires an active session", record.summary.ID, status)
	}
	if mailbox == nil {
		return MessageReceipt{}, fmt.Errorf("daemon: agent %q has no live mailbox", record.summary.ID)
	}
	message := tools.QueuedMessage{
		ID:        uuid.NewString(),
		Mode:      normalizedMode,
		Content:   content,
		Source:    options.Source,
		From:      options.From,
		CreatedAt: time.Now().UTC(),
	}
	deliveryStatus, err := mailbox.Enqueue(message)
	if err != nil {
		return MessageReceipt{}, err
	}
	return MessageReceipt{
		MessageID: message.ID,
		SessionID: record.summary.ID,
		Mode:      message.Mode,
		Status:    deliveryStatus,
	}, nil
}

func (manager *Manager) Inbox(id string) ([]tools.QueuedMessage, error) {
	return manager.InboxInNamespace("", id)
}

func (manager *Manager) InboxInNamespace(namespace, id string) ([]tools.QueuedMessage, error) {
	if strings.TrimSpace(namespace) == "" {
		return manager.inboxInNamespace("", id)
	}
	normalized, err := session.NormalizeNamespace(namespace)
	if err != nil {
		return nil, err
	}
	return manager.inboxInNamespace(normalized, id)
}

func (manager *Manager) inboxInNamespace(namespace, id string) ([]tools.QueuedMessage, error) {
	manager.mu.RLock()
	record, err := manager.findLocked(namespace, id)
	if err != nil {
		manager.mu.RUnlock()
		return nil, err
	}
	mailbox := record.mailbox
	manager.mu.RUnlock()
	if mailbox == nil {
		return nil, fmt.Errorf("daemon: agent %q has no live mailbox", record.summary.ID)
	}
	return mailbox.Snapshot(), nil
}

func normalizeMessageMode(mode tools.MessageMode) (tools.MessageMode, error) {
	switch strings.ToLower(strings.TrimSpace(string(mode))) {
	case "", string(tools.MessageModeSteer):
		return tools.MessageModeSteer, nil
	case "follow-up", "followup", string(tools.MessageModeFollowUp):
		return tools.MessageModeFollowUp, nil
	default:
		return "", fmt.Errorf("daemon: unsupported message mode %q", mode)
	}
}

func (manager *Manager) Compact(id string, wait bool) (CompactResult, error) {
	return manager.compactInNamespace("", id, wait)
}

func (manager *Manager) CompactInNamespace(namespace, id string, wait bool) (CompactResult, error) {
	if strings.TrimSpace(namespace) == "" {
		return manager.compactInNamespace("", id, wait)
	}
	normalized, err := session.NormalizeNamespace(namespace)
	if err != nil {
		return CompactResult{}, err
	}
	return manager.compactInNamespace(normalized, id, wait)
}

func (manager *Manager) compactInNamespace(namespace, id string, wait bool) (CompactResult, error) {
	manager.mu.Lock()
	record, err := manager.findLocked(namespace, id)
	if err != nil {
		manager.mu.Unlock()
		return CompactResult{}, err
	}
	active := record.summary.Status == "running" || record.summary.Status == "idle" || record.summary.Status == "waiting"
	if !active {
		if command := record.lastCompaction; command != nil {
			manager.mu.Unlock()
			if wait {
				command.wait()
			}
			return command.snapshot(), nil
		}
		status := record.summary.Status
		manager.mu.Unlock()
		return CompactResult{}, fmt.Errorf("daemon: agent %q is %s; context compaction requires an active session", record.summary.ID, status)
	}
	if record.compaction == nil && record.lastCompaction != nil {
		command := record.lastCompaction
		manager.mu.Unlock()
		if wait {
			command.wait()
		}
		return command.snapshot(), nil
	}
	if command := record.compaction; command != nil {
		manager.mu.Unlock()
		if wait {
			command.wait()
		}
		return command.snapshot(), nil
	}
	if record.control == nil {
		manager.mu.Unlock()
		return CompactResult{}, fmt.Errorf("daemon: agent %q does not support context controls", record.summary.ID)
	}
	command := newCompactionCommand(record.summary.ID)
	record.compaction = command
	record.lastCompaction = command
	request := agent.ControlRequest{
		Kind: agent.ControlContextCompact,
		Complete: func(result agent.ControlResult) {
			manager.finishCompaction(record, command, result)
		},
	}
	manager.mu.Unlock()

	if err := manager.appendEvent(record, session.NewEvent(session.EventContextCompactionQueued, map[string]any{
		"command_id": command.snapshot().CommandID,
	})); err != nil {
		manager.mu.Lock()
		record.persistenceErr = err
		manager.mu.Unlock()
	}

	manager.mu.Lock()
	if record.compaction == command &&
		(record.summary.Status == "running" || record.summary.Status == "idle" || record.summary.Status == "waiting") {
		record.control <- request
	}
	manager.mu.Unlock()

	if wait {
		command.wait()
	}
	return command.snapshot(), nil
}

func (manager *Manager) finishCompaction(
	record *managedAgent,
	command *compactionCommand,
	result agent.ControlResult,
) {
	command.complete(result)
	manager.mu.Lock()
	if record.compaction == command {
		record.compaction = nil
	}
	record.lastCompaction = command
	manager.mu.Unlock()
	data := map[string]any{
		"command_id": command.snapshot().CommandID,
		"status":     string(result.Status),
	}
	if result.Error != "" {
		data["error"] = result.Error
	}
	if err := manager.appendEvent(record, session.NewEvent(session.EventContextCompactionFinished, data)); err != nil {
		manager.mu.Lock()
		record.persistenceErr = err
		manager.mu.Unlock()
	}
}

func (manager *Manager) failCompaction(record *managedAgent, command *compactionCommand, message string) {
	command.fail(message)
	manager.mu.Lock()
	if record.compaction == command {
		record.compaction = nil
	}
	record.lastCompaction = command
	manager.mu.Unlock()
	data := map[string]any{
		"command_id": command.snapshot().CommandID,
		"status":     "failed",
		"error":      message,
	}
	if err := manager.appendEvent(record, session.NewEvent(session.EventContextCompactionFinished, data)); err != nil {
		manager.mu.Lock()
		record.persistenceErr = err
		manager.mu.Unlock()
	}
}

func (manager *Manager) Kill(id string) (AgentSummary, error) {
	return manager.killInNamespace("", id)
}

func (manager *Manager) KillInNamespace(namespace, id string) (AgentSummary, error) {
	normalized, err := session.NormalizeNamespace(namespace)
	if err != nil {
		return AgentSummary{}, err
	}
	return manager.killInNamespace(normalized, id)
}

func (manager *Manager) killInNamespace(namespace, id string) (AgentSummary, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	record, err := manager.findLocked(namespace, id)
	if err != nil {
		return AgentSummary{}, err
	}
	if record.summary.Status != "running" &&
		record.summary.Status != "waiting" &&
		record.summary.Status != "idle" &&
		record.summary.Status != "stopping" {
		return AgentSummary{}, fmt.Errorf("daemon: agent %q is already %s", record.summary.ID, record.summary.Status)
	}
	record.cancelRequested = true
	record.summary.Status = "stopping"
	if record.cancel != nil {
		record.cancel()
	}
	return record.summary, nil
}

func (manager *Manager) findLocked(namespace, id string) (*managedAgent, error) {
	namespace, id = splitTarget(namespace, id)
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("daemon: agent ID is required")
	}
	var exact *managedAgent
	var matches []*managedAgent
	for _, record := range manager.agents {
		if namespace != "" && record.summary.Namespace != namespace {
			continue
		}
		if record.summary.ID == id {
			exact = record
			break
		}
		if strings.HasPrefix(record.summary.ID, id) {
			matches = append(matches, record)
		}
	}
	if exact != nil {
		return exact, nil
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("daemon: agent ID prefix %q is ambiguous", id)
	}
	return nil, fmt.Errorf("daemon: agent %q not found", id)
}

func splitTarget(namespace, id string) (string, string) {
	if index := strings.IndexByte(id, '/'); index >= 0 {
		namespace = id[:index]
		id = id[index+1:]
	}
	return namespace, id
}

func summaryFromSession(item session.Summary) AgentSummary {
	return AgentSummary{
		ID:         item.Header.ID,
		Namespace:  item.Header.Namespace,
		Model:      item.Header.Model,
		Task:       item.Header.Task,
		Workspace:  item.Header.Workspace,
		Status:     item.Status,
		Turns:      item.Turns,
		Final:      item.Final,
		Error:      item.Error,
		StartedAt:  item.Header.StartedAt,
		FinishedAt: item.FinishedAt,
	}
}
