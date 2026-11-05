package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentsd/internal/llm"
)

// RegistryOptions controls which registered tools are visible to the model.
type RegistryOptions struct {
	Enabled []string
}

// Registry stores the tools available to one runtime.
type Registry struct {
	tools       map[string]Tool
	factories   map[string]ToolFactory
	definitions []llm.Tool
	specs       map[string]ToolSpec
	enabled     map[string]struct{}
}

func NewRegistry(toolList ...Tool) (Registry, error) {
	return NewRegistryWithOptions(RegistryOptions{}, toolList...)
}

func NewRegistryWithOptions(options RegistryOptions, toolList ...Tool) (Registry, error) {
	factories := make([]ToolFactory, len(toolList))
	for index, tool := range toolList {
		if tool == nil {
			return Registry{}, fmt.Errorf("tools: tool is nil")
		}
		registeredTool := tool
		factories[index] = func(_ SessionContext) (Tool, error) {
			return registeredTool, nil
		}
	}
	return NewFactoryRegistryWithOptions(options, factories...)
}

func NewFactoryRegistry(factories ...ToolFactory) (Registry, error) {
	return NewFactoryRegistryWithOptions(RegistryOptions{}, factories...)
}

func NewFactoryRegistryWithOptions(options RegistryOptions, factories ...ToolFactory) (Registry, error) {
	registry := Registry{
		tools:       make(map[string]Tool, len(factories)),
		factories:   make(map[string]ToolFactory, len(factories)),
		definitions: make([]llm.Tool, 0, len(factories)),
		specs:       make(map[string]ToolSpec, len(factories)),
	}
	if len(options.Enabled) > 0 {
		registry.enabled = make(map[string]struct{}, len(options.Enabled))
		for _, name := range options.Enabled {
			normalized := normalizeName(name)
			if normalized == "" {
				return Registry{}, fmt.Errorf("tools: enabled tool name is required")
			}
			registry.enabled[normalized] = struct{}{}
		}
	}

	for _, factory := range factories {
		if factory == nil {
			return Registry{}, fmt.Errorf("tools: tool factory is nil")
		}
		tool, err := factory(SessionContext{})
		if err != nil {
			return Registry{}, fmt.Errorf("tools: create tool prototype: %w", err)
		}
		if tool == nil {
			return Registry{}, fmt.Errorf("tools: tool factory returned nil")
		}
		spec, err := validateSpec(tool.Spec())
		if err != nil {
			return Registry{}, err
		}
		if _, exists := registry.tools[spec.Name]; exists {
			return Registry{}, fmt.Errorf("tools: duplicate tool %q", spec.Name)
		}
		registry.tools[spec.Name] = tool
		registry.factories[spec.Name] = factory
		registry.specs[spec.Name] = spec
		registry.definitions = append(registry.definitions, spec.Definition())
	}
	for name := range registry.enabled {
		if _, exists := registry.tools[name]; !exists {
			return Registry{}, fmt.Errorf("tools: enabled tool %q is not registered", name)
		}
	}
	return registry, nil
}

func validateSpec(spec ToolSpec) (ToolSpec, error) {
	spec.Name = normalizeName(spec.Name)
	if spec.Name == "" {
		return ToolSpec{}, fmt.Errorf("tools: tool name is required")
	}
	if strings.TrimSpace(spec.Description) == "" {
		return ToolSpec{}, fmt.Errorf("tools: tool %q description is required", spec.Name)
	}
	if spec.Decode == nil {
		return ToolSpec{}, fmt.Errorf("tools: tool %q argument decoder is required", spec.Name)
	}
	if !validParameters(spec.Parameters) {
		return ToolSpec{}, fmt.Errorf("tools: tool %q parameters must be a JSON object", spec.Name)
	}
	spec.Policy = spec.Policy.normalized()
	if spec.Policy.Timeout < 0 {
		return ToolSpec{}, fmt.Errorf("tools: tool %q timeout cannot be negative", spec.Name)
	}
	if spec.Policy.Tier != TierRead && spec.Policy.Tier != TierWrite && spec.Policy.Tier != TierExec {
		return ToolSpec{}, fmt.Errorf("tools: tool %q has invalid tier %q", spec.Name, spec.Policy.Tier)
	}
	if spec.Policy.Concurrency != ConcurrencyShared && spec.Policy.Concurrency != ConcurrencyExclusive {
		return ToolSpec{}, fmt.Errorf("tools: tool %q has invalid concurrency %q", spec.Name, spec.Policy.Concurrency)
	}
	spec.Parameters = append(json.RawMessage(nil), spec.Parameters...)
	return spec, nil
}

func normalizeName(name string) string {
	return strings.TrimSpace(name)
}

func validParameters(parameters json.RawMessage) bool {
	var value map[string]any
	return len(parameters) > 0 && json.Unmarshal(parameters, &value) == nil && value != nil
}

func cloneDefinition(definition llm.Tool) llm.Tool {
	definition.Function.Parameters = append(json.RawMessage(nil), definition.Function.Parameters...)
	return definition
}

func (r Registry) Definitions() []llm.Tool {
	definitions := make([]llm.Tool, 0, len(r.definitions))
	for _, definition := range r.definitions {
		if !r.visible(definition.Function.Name) {
			continue
		}
		definitions = append(definitions, cloneDefinition(definition))
	}
	return definitions
}

func (r Registry) Lookup(name string) (Tool, bool) {
	tool, ok := r.tools[normalizeName(name)]
	return tool, ok
}

func (r Registry) Resolve(name string, session SessionContext) (Tool, error) {
	normalized := normalizeName(name)
	factory, ok := r.factories[normalized]
	if !ok {
		return nil, fmt.Errorf("tools: tool %q is not registered", normalized)
	}
	tool, err := factory(session)
	if err != nil {
		return nil, fmt.Errorf("tools: create tool %q: %w", normalized, err)
	}
	if tool == nil {
		return nil, fmt.Errorf("tools: factory for tool %q returned nil", normalized)
	}
	spec, err := validateSpec(tool.Spec())
	if err != nil {
		return nil, err
	}
	if spec.Name != normalized {
		return nil, fmt.Errorf("tools: factory returned tool %q for %q", spec.Name, normalized)
	}
	return tool, nil
}

func (r Registry) Visible(name string) bool {
	return r.visible(normalizeName(name))
}

func (r Registry) visible(name string) bool {
	spec, ok := r.specs[name]
	if !ok || spec.Hidden {
		return false
	}
	if r.enabled == nil {
		return true
	}
	_, ok = r.enabled[name]
	return ok
}

func (r Registry) Spec(name string) (ToolSpec, bool) {
	spec, ok := r.specs[normalizeName(name)]
	if !ok {
		return ToolSpec{}, false
	}
	spec.Parameters = append(json.RawMessage(nil), spec.Parameters...)
	return spec, true
}
