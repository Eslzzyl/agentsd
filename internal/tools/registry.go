package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentsd/internal/llm"
)

// Tool is an executable function exposed to the model.
type Tool interface {
	Definition() llm.Tool
	Execute(ctx context.Context, arguments json.RawMessage) (string, error)
}

// Registry stores the tools available to one runtime.
type Registry struct {
	tools       map[string]Tool
	definitions []llm.Tool
}

func NewRegistry(tools ...Tool) (Registry, error) {
	registry := Registry{
		tools:       make(map[string]Tool, len(tools)),
		definitions: make([]llm.Tool, 0, len(tools)),
	}
	for _, tool := range tools {
		if tool == nil {
			return Registry{}, fmt.Errorf("tools: tool is nil")
		}
		definition := tool.Definition()
		if definition.Type != "function" {
			return Registry{}, fmt.Errorf("tools: tool %q has type %q, want function", definition.Function.Name, definition.Type)
		}
		name := strings.TrimSpace(definition.Function.Name)
		if name == "" {
			return Registry{}, fmt.Errorf("tools: tool name is required")
		}
		if _, exists := registry.tools[name]; exists {
			return Registry{}, fmt.Errorf("tools: duplicate tool %q", name)
		}
		registry.tools[name] = tool
		definition.Function.Name = name
		registry.definitions = append(registry.definitions, definition)
	}
	return registry, nil
}

func (r Registry) Definitions() []llm.Tool {
	return append([]llm.Tool(nil), r.definitions...)
}

func (r Registry) Lookup(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}
