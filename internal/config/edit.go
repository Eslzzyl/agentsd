package config

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type configValueKind uint8

const (
	configString configValueKind = iota
	configBool
	configInt
	configInt64
	configStringList
	configAny
)

type configFieldSpec struct {
	path string
	kind configValueKind
}

var configFieldSpecs = []configFieldSpec{
	{path: "provider", kind: configString},
	{path: "model", kind: configString},
	{path: "thinking_level", kind: configString},
	{path: "providers/*/api_type", kind: configString},
	{path: "providers/*/base_url", kind: configString},
	{path: "providers/*/models/*/api_type", kind: configString},
	{path: "providers/*/models/*/base_url", kind: configString},
	{path: "providers/*/models/*/request_model_id", kind: configString},
	{path: "providers/*/models/*/context_window", kind: configInt},
	{path: "providers/*/models/*/max_output_tokens", kind: configInt},
	{path: "providers/*/models/*/supports_images", kind: configBool},
	{path: "providers/*/models/*/supports_streaming", kind: configBool},
	{path: "providers/*/models/*/extra_body", kind: configAny},
	{path: "providers/*/models/*/extra_body/*", kind: configAny},
	{path: "search/enabled", kind: configBool},
	{path: "search/timeout_seconds", kind: configInt},
	{path: "search/max_results", kind: configInt},
	{path: "search/order", kind: configStringList},
	{path: "search/providers/*/enabled", kind: configBool},
	{path: "search/providers/*/endpoint", kind: configString},
	{path: "search/providers/*/auth_ref", kind: configString},
	{path: "search/providers/*/model", kind: configString},
	{path: "search/providers/*/options", kind: configAny},
	{path: "search/providers/*/options/*", kind: configAny},
	{path: "fetch/enabled", kind: configBool},
	{path: "fetch/timeout_seconds", kind: configInt},
	{path: "fetch/max_bytes", kind: configInt64},
}

func SetConfigValue(cfg Config, path, raw string) (Config, error) {
	segments, err := splitConfigPath(path)
	if err != nil {
		return Config{}, err
	}
	kind, ok := configFieldKind(segments)
	if !ok {
		return Config{}, fmt.Errorf("config: unsupported field path %q", path)
	}
	value, err := parseConfigValue(raw, kind)
	if err != nil {
		return Config{}, fmt.Errorf("config: parse %q: %w", path, err)
	}
	encoded, err := toml.Marshal(cfg)
	if err != nil {
		return Config{}, fmt.Errorf("config: encode current config: %w", err)
	}
	var tree map[string]any
	if err := toml.Unmarshal(encoded, &tree); err != nil {
		return Config{}, fmt.Errorf("config: decode current config: %w", err)
	}
	if err := setConfigTreeValue(tree, segments, value); err != nil {
		return Config{}, err
	}
	encoded, err = toml.Marshal(tree)
	if err != nil {
		return Config{}, fmt.Errorf("config: encode updated config: %w", err)
	}
	updated, err := ParseTOML(encoded)
	if err != nil {
		return Config{}, err
	}
	return updated, nil
}

func splitConfigPath(path string) ([]string, error) {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil, fmt.Errorf("config: field path is required")
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if strings.TrimSpace(part) == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("config: invalid field path %q", path)
		}
	}
	return parts, nil
}

func configFieldKind(path []string) (configValueKind, bool) {
	for _, spec := range configFieldSpecs {
		pattern := strings.Split(spec.path, "/")
		if len(pattern) != len(path) {
			continue
		}
		matches := true
		for index := range pattern {
			if pattern[index] != "*" && pattern[index] != path[index] {
				matches = false
				break
			}
		}
		if matches {
			return spec.kind, true
		}
	}
	return 0, false
}

func parseConfigValue(raw string, kind configValueKind) (any, error) {
	raw = strings.TrimSpace(raw)
	switch kind {
	case configString:
		if raw == "" {
			return nil, fmt.Errorf("string value is required")
		}
		return raw, nil
	case configBool:
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("expected true or false")
		}
		return value, nil
	case configInt:
		value, err := strconv.ParseInt(raw, 10, 0)
		if err != nil {
			return nil, fmt.Errorf("expected integer")
		}
		return int(value), nil
	case configInt64:
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("expected integer")
		}
		return value, nil
	case configStringList:
		return parseStringList(raw)
	case configAny:
		value, err := parseTOMLValue(raw)
		if err == nil {
			return value, nil
		}
		if raw == "" {
			return nil, fmt.Errorf("value is required")
		}
		return raw, nil
	default:
		return nil, fmt.Errorf("unsupported value type")
	}
}

func parseStringList(raw string) ([]string, error) {
	if !strings.HasPrefix(raw, "[") {
		parts := strings.Split(raw, ",")
		values := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				return nil, fmt.Errorf("list contains an empty item")
			}
			values = append(values, part)
		}
		return values, nil
	}
	value, err := parseTOMLValue(raw)
	if err != nil {
		return nil, err
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("expected a string list")
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("expected a string list")
		}
		result = append(result, text)
	}
	return result, nil
}

func parseTOMLValue(raw string) (any, error) {
	var values map[string]any
	if err := toml.Unmarshal([]byte("value = "+raw+"\n"), &values); err != nil {
		return nil, err
	}
	value, ok := values["value"]
	if !ok {
		return nil, fmt.Errorf("value is required")
	}
	return value, nil
}

func setConfigTreeValue(tree map[string]any, path []string, value any) error {
	current := tree
	for _, segment := range path[:len(path)-1] {
		next, ok := current[segment]
		if !ok {
			child := make(map[string]any)
			current[segment] = child
			current = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("config: field path %q crosses a scalar value", strings.Join(path, "/"))
		}
		current = child
	}
	current[path[len(path)-1]] = value
	return nil
}
