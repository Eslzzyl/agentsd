package tools

import (
	"context"
	"encoding/json"
	"testing"
)

type registryTestTool struct {
	spec ToolSpec
}

func (tool registryTestTool) Spec() ToolSpec {
	return tool.spec
}

func (registryTestTool) Execute(context.Context, ExecutionContext, any) (ToolResult, error) {
	return ToolResult{Content: "ok"}, nil
}

func TestRegistryValidatesAndCopiesDefinitions(t *testing.T) {
	parameters := json.RawMessage(`{"type":"object"}`)
	registry, err := NewRegistry(registryTestTool{spec: ToolSpec{
		Name:        "lookup",
		Description: "Look up a value.",
		Parameters:  parameters,
		Decode: func(arguments json.RawMessage) (any, error) {
			return arguments, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	definitions := registry.Definitions()
	if len(definitions) != 1 || definitions[0].Function.Name != "lookup" {
		t.Fatalf("definitions = %#v", definitions)
	}
	definitions[0].Function.Parameters[0] = 'x'
	if string(registry.Definitions()[0].Function.Parameters) != string(parameters) {
		t.Fatalf("definition parameters were not copied")
	}
	spec, ok := registry.Spec("lookup")
	if !ok || spec.Policy.Tier != TierExec || spec.Policy.Concurrency != ConcurrencyShared {
		t.Fatalf("spec = %#v, ok = %v", spec, ok)
	}
}

func TestRegistryRejectsIncompleteToolSpec(t *testing.T) {
	cases := []ToolSpec{
		{Name: "missing-description", Parameters: json.RawMessage(`{"type":"object"}`), Decode: func(json.RawMessage) (any, error) { return nil, nil }},
		{Name: "missing-parameters", Description: "test", Decode: func(json.RawMessage) (any, error) { return nil, nil }},
		{Name: "missing-decoder", Description: "test", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	for _, spec := range cases {
		if _, err := NewRegistry(registryTestTool{spec: spec}); err == nil {
			t.Fatalf("spec %#v was accepted", spec)
		}
	}
}
func TestRegistryFiltersHiddenAndDisabledTools(t *testing.T) {
	makeSpec := func(name string, hidden bool) ToolSpec {
		return ToolSpec{
			Name:        name,
			Description: "test tool",
			Parameters:  json.RawMessage(`{"type":"object"}`),
			Hidden:      hidden,
			Decode: func(json.RawMessage) (any, error) {
				return nil, nil
			},
		}
	}
	registry, err := NewRegistryWithOptions(
		RegistryOptions{Enabled: []string{" visible "}},
		registryTestTool{spec: makeSpec("visible", false)},
		registryTestTool{spec: makeSpec("hidden", true)},
	)
	if err != nil {
		t.Fatal(err)
	}
	definitions := registry.Definitions()
	if len(definitions) != 1 || definitions[0].Function.Name != "visible" {
		t.Fatalf("definitions = %#v", definitions)
	}
	if registry.Visible("hidden") || !registry.Visible("visible") {
		t.Fatalf("unexpected hidden visibility")
	}
	if !registry.Visible(" visible ") {
		t.Fatalf("normalized enabled name is not visible")
	}
	if _, ok := registry.Lookup("hidden"); !ok {
		t.Fatalf("hidden tool is not registered")
	}
}

func TestFactoryRegistryCreatesSessionToolInstances(t *testing.T) {
	var prototypeCalls int
	var resolvedSession string
	registry, err := NewFactoryRegistry(func(session SessionContext) (Tool, error) {
		if session.ID == "" {
			prototypeCalls++
		} else {
			resolvedSession = session.ID
		}
		return registryTestTool{spec: ToolSpec{
			Name:        "session-tool",
			Description: "session tool",
			Parameters:  json.RawMessage(`{"type":"object"}`),
			Decode: func(json.RawMessage) (any, error) {
				return nil, nil
			},
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if prototypeCalls != 1 {
		t.Fatalf("prototype calls = %d", prototypeCalls)
	}
	if _, err := registry.Resolve(" session-tool ", SessionContext{ID: "session-1"}); err != nil {
		t.Fatal(err)
	}
	if resolvedSession != "session-1" {
		t.Fatalf("resolved session = %q", resolvedSession)
	}
}
