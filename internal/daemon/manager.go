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

type AgentSummary struct {
	ID         string     `json:"id"`
	Namespace  string     `json:"namespace"`
	Model      string     `json:"model"`
	Task       string     `json:"task"`
	Workspace  string     `json:"workspace"`
	Status     string     `json:"status"`
	Turns      int        `json:"turns"`
	Final      string     `json:"final,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type NamespaceResult struct {
	Name string `json:"name"`
}

type NamespaceRenameResult struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type managedAgent struct {
	summary         AgentSummary
	cancel          context.CancelFunc
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
		cancel: cancel,
	}
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
	summary := record.summary
	manager.mu.RUnlock()
	result, runErr := runner.Run(ctx, agent.RunRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are a managed coding agent. Execute the task with the available tools and return a concise final report."},
			{Role: llm.RoleUser, Content: summary.Task},
		},
		Session: &tools.SessionContext{
			ID:            summary.ID,
			Namespace:     summary.Namespace,
			CWD:           summary.Workspace,
			WorkspaceRoot: summary.Workspace,
		},
		EventSink: func(event tools.Event) {
			if err := manager.appendEvent(record, eventToSession(event)); err != nil {
				manager.mu.Lock()
				record.persistenceErr = err
				manager.mu.Unlock()
			}
		},
		ContextEventSink: func(event agent.ContextEvent) {
			if err := manager.appendEvent(record, contextEventToSession(event)); err != nil {
				manager.mu.Lock()
				record.persistenceErr = err
				manager.mu.Unlock()
			}
		},
	})
	finishedAt := time.Now().UTC()

	manager.mu.Lock()
	record.summary.Turns = result.Turns
	record.summary.FinishedAt = &finishedAt
	cancelRequested := record.cancelRequested
	persistenceErr := record.persistenceErr
	if runErr != nil {
		record.summary.Error = runErr.Error()
		if cancelRequested || errors.Is(runErr, context.Canceled) {
			record.summary.Status = "canceled"
		} else {
			record.summary.Status = "failed"
		}
	} else {
		record.summary.Status = "completed"
		record.summary.Final = result.FinalMessage.Content
	}
	finalSummary := record.summary
	manager.mu.Unlock()

	eventType := session.EventAgentCompleted
	fields := map[string]any{
		"finished_at": finishedAt,
		"turns":       finalSummary.Turns,
		"final":       finalSummary.Final,
	}
	if finalSummary.Status == "canceled" {
		eventType = session.EventAgentCanceled
	}
	if finalSummary.Status == "failed" {
		eventType = session.EventAgentFailed
	}
	if finalSummary.Error != "" {
		fields["error"] = finalSummary.Error
	}
	if persistenceErr != nil {
		fields["persistence_error"] = persistenceErr.Error()
	}
	if err := manager.appendEvent(record, session.NewEvent(eventType, fields)); err != nil {
		manager.mu.Lock()
		record.summary.Status = "failed"
		record.summary.Error = err.Error()
		manager.mu.Unlock()
	}
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
		result = append(result, record.summary)
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
	result := record.summary
	manager.mu.RUnlock()
	return result, nil
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
	if record.summary.Status != "running" && record.summary.Status != "stopping" {
		return AgentSummary{}, fmt.Errorf("daemon: agent %q is already %s", record.summary.ID, record.summary.Status)
	}
	record.cancelRequested = true
	record.summary.Status = "stopping"
	record.cancel()
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
