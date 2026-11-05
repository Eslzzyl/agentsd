package daemon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"agentsd/internal/agent"
	"agentsd/internal/llm"
)

type Runner interface {
	Run(context.Context, agent.RunRequest) (agent.RunResult, error)
}

type RuntimeFactory func(model string) (Runner, string, error)

type AgentSummary struct {
	ID         string     `json:"id"`
	Model      string     `json:"model"`
	Task       string     `json:"task"`
	Status     string     `json:"status"`
	Turns      int        `json:"turns"`
	Final      string     `json:"final,omitempty"`
	Error      string     `json:"error,omitempty"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type managedAgent struct {
	summary         AgentSummary
	cancel          context.CancelFunc
	cancelRequested bool
}

type Manager struct {
	mu      sync.RWMutex
	nextID  uint64
	agents  map[string]*managedAgent
	factory RuntimeFactory
}

func NewManager(factory RuntimeFactory) (*Manager, error) {
	if factory == nil {
		return nil, fmt.Errorf("daemon: runtime factory is required")
	}
	return &Manager{agents: make(map[string]*managedAgent), factory: factory}, nil
}

func (manager *Manager) Spawn(model, task string) (AgentSummary, error) {
	if strings.TrimSpace(task) == "" {
		return AgentSummary{}, fmt.Errorf("daemon: task is required")
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
	ctx, cancel := context.WithCancel(context.Background())
	manager.mu.Lock()
	manager.nextID++
	id := fmt.Sprintf("a%04d", manager.nextID)
	record := &managedAgent{
		summary: AgentSummary{
			ID:        id,
			Model:     resolvedModel,
			Task:      task,
			Status:    "running",
			StartedAt: time.Now().UTC(),
		},
		cancel: cancel,
	}
	manager.agents[id] = record
	summary := record.summary
	manager.mu.Unlock()

	go manager.run(ctx, record, runner, resolvedModel)
	return summary, nil
}

func (manager *Manager) run(ctx context.Context, record *managedAgent, runner Runner, model string) {
	result, err := runner.Run(ctx, agent.RunRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are a managed coding agent. Execute the task with the available tools and return a concise final report."},
			{Role: llm.RoleUser, Content: record.summary.Task},
		},
	})
	finishedAt := time.Now().UTC()

	manager.mu.Lock()
	defer manager.mu.Unlock()
	record.summary.Turns = result.Turns
	record.summary.FinishedAt = &finishedAt
	if err != nil {
		if record.cancelRequested || errors.Is(err, context.Canceled) {
			record.summary.Status = "canceled"
		} else {
			record.summary.Status = "failed"
		}
		record.summary.Error = err.Error()
		return
	}
	record.summary.Status = "completed"
	record.summary.Final = result.FinalMessage.Content
}

func (manager *Manager) List() []AgentSummary {
	manager.mu.RLock()
	result := make([]AgentSummary, 0, len(manager.agents))
	for _, record := range manager.agents {
		result = append(result, record.summary)
	}
	manager.mu.RUnlock()
	sort.Slice(result, func(left, right int) bool { return result[left].ID < result[right].ID })
	return result
}

func (manager *Manager) Inspect(id string) (AgentSummary, error) {
	manager.mu.RLock()
	record, ok := manager.agents[id]
	if !ok {
		manager.mu.RUnlock()
		return AgentSummary{}, fmt.Errorf("daemon: agent %q not found", id)
	}
	result := record.summary
	manager.mu.RUnlock()
	return result, nil
}

func (manager *Manager) Kill(id string) (AgentSummary, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	record, ok := manager.agents[id]
	if !ok {
		return AgentSummary{}, fmt.Errorf("daemon: agent %q not found", id)
	}
	if record.summary.Status != "running" && record.summary.Status != "stopping" {
		return AgentSummary{}, fmt.Errorf("daemon: agent %q is already %s", id, record.summary.Status)
	}
	record.cancelRequested = true
	record.summary.Status = "stopping"
	record.cancel()
	return record.summary, nil
}
