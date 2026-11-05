package llm

import "strings"

type ThinkingLevel string

const (
	ThinkingOff  ThinkingLevel = "off"
	ThinkingHigh ThinkingLevel = "high"
	ThinkingMax  ThinkingLevel = "max"
)

func (level ThinkingLevel) DisplayName() string {
	return displayThinkingLevel(string(level))
}

func (level ThinkingLevel) Next() ThinkingLevel {
	return ThinkingLevel(cycleThinkingLevel(string(level), []string{"off", "high", "max"}))
}

type DeepSeekV4ThinkingLevel string

type Qwen35ThinkingLevel string

type Qwen38ThinkingLevel string

type GlmThinkingLevel string

type Gpt5ThinkingLevel string

type MiniMaxThinkingLevel string

type ClaudeEffortLevel string

type MuseSparkThinkingLevel string

const (
	DeepSeekV4Off  DeepSeekV4ThinkingLevel = "off"
	DeepSeekV4High DeepSeekV4ThinkingLevel = "high"
	DeepSeekV4Max  DeepSeekV4ThinkingLevel = "max"

	Qwen35Off Qwen35ThinkingLevel = "off"
	Qwen35On  Qwen35ThinkingLevel = "on"

	Qwen38Off    Qwen38ThinkingLevel = "off"
	Qwen38Low    Qwen38ThinkingLevel = "low"
	Qwen38Medium Qwen38ThinkingLevel = "medium"
	Qwen38XHigh  Qwen38ThinkingLevel = "xhigh"

	GLMOff  GlmThinkingLevel = "off"
	GLMHigh GlmThinkingLevel = "high"
	GLMMax  GlmThinkingLevel = "max"

	GPT5Off    Gpt5ThinkingLevel = "off"
	GPT5Low    Gpt5ThinkingLevel = "low"
	GPT5Medium Gpt5ThinkingLevel = "medium"
	GPT5High   Gpt5ThinkingLevel = "high"
	GPT5XHigh  Gpt5ThinkingLevel = "xhigh"
	GPT5Max    Gpt5ThinkingLevel = "max"

	MiniMaxOff  MiniMaxThinkingLevel = "off"
	MiniMaxHigh MiniMaxThinkingLevel = "high"
	MiniMaxMax  MiniMaxThinkingLevel = "max"

	ClaudeOff    ClaudeEffortLevel = "off"
	ClaudeLow    ClaudeEffortLevel = "low"
	ClaudeMedium ClaudeEffortLevel = "medium"
	ClaudeHigh   ClaudeEffortLevel = "high"

	MuseSparkMinimal MuseSparkThinkingLevel = "minimal"
	MuseSparkLow     MuseSparkThinkingLevel = "low"
	MuseSparkMedium  MuseSparkThinkingLevel = "medium"
	MuseSparkHigh    MuseSparkThinkingLevel = "high"
	MuseSparkXHigh   MuseSparkThinkingLevel = "xhigh"
)

func ParseDeepSeekV4ThinkingLevel(value string) DeepSeekV4ThinkingLevel {
	return DeepSeekV4ThinkingLevel(parseLevel(value, []string{"off", "high", "max"}, "off"))
}

func ParseQwen35ThinkingLevel(value string) Qwen35ThinkingLevel {
	return Qwen35ThinkingLevel(parseLevel(value, []string{"off", "on"}, "off"))
}

func ParseQwen38ThinkingLevel(value string) Qwen38ThinkingLevel {
	return Qwen38ThinkingLevel(parseLevel(value, []string{"off", "low", "medium", "xhigh"}, "off"))
}

func ParseGlmThinkingLevel(value string) GlmThinkingLevel {
	return GlmThinkingLevel(parseLevel(value, []string{"off", "high", "max"}, "off"))
}

func ParseGpt5ThinkingLevel(value string) Gpt5ThinkingLevel {
	return Gpt5ThinkingLevel(parseLevel(value, []string{"off", "low", "medium", "high", "xhigh", "max"}, "off"))
}

func ParseMiniMaxThinkingLevel(value string) MiniMaxThinkingLevel {
	return MiniMaxThinkingLevel(parseLevel(value, []string{"off", "high", "max"}, "off"))
}

func ParseClaudeEffortLevel(value string) ClaudeEffortLevel {
	return ClaudeEffortLevel(parseLevel(value, []string{"off", "low", "medium", "high"}, "off"))
}

func ParseMuseSparkThinkingLevel(value string) MuseSparkThinkingLevel {
	return MuseSparkThinkingLevel(parseLevel(value, []string{"minimal", "low", "medium", "high", "xhigh"}, "medium"))
}

func (level DeepSeekV4ThinkingLevel) DisplayName() string { return displayThinkingLevel(string(level)) }
func (level Qwen35ThinkingLevel) DisplayName() string     { return displayThinkingLevel(string(level)) }
func (level Qwen38ThinkingLevel) DisplayName() string     { return displayThinkingLevel(string(level)) }
func (level GlmThinkingLevel) DisplayName() string        { return displayThinkingLevel(string(level)) }
func (level Gpt5ThinkingLevel) DisplayName() string       { return displayThinkingLevel(string(level)) }
func (level MiniMaxThinkingLevel) DisplayName() string    { return displayThinkingLevel(string(level)) }
func (level ClaudeEffortLevel) DisplayName() string       { return displayThinkingLevel(string(level)) }
func (level MuseSparkThinkingLevel) DisplayName() string  { return displayThinkingLevel(string(level)) }

func (level DeepSeekV4ThinkingLevel) Next() DeepSeekV4ThinkingLevel {
	return DeepSeekV4ThinkingLevel(cycleThinkingLevel(string(level), []string{"off", "high", "max"}))
}
func (level Qwen35ThinkingLevel) Next() Qwen35ThinkingLevel {
	return Qwen35ThinkingLevel(cycleThinkingLevel(string(level), []string{"off", "on"}))
}
func (level Qwen38ThinkingLevel) Next() Qwen38ThinkingLevel {
	return Qwen38ThinkingLevel(cycleThinkingLevel(string(level), []string{"off", "low", "medium", "xhigh"}))
}
func (level GlmThinkingLevel) Next() GlmThinkingLevel {
	return GlmThinkingLevel(cycleThinkingLevel(string(level), []string{"off", "high", "max"}))
}
func (level Gpt5ThinkingLevel) Next() Gpt5ThinkingLevel {
	return Gpt5ThinkingLevel(cycleThinkingLevel(string(level), []string{"off", "low", "medium", "high", "xhigh", "max"}))
}
func (level MiniMaxThinkingLevel) Next() MiniMaxThinkingLevel {
	return MiniMaxThinkingLevel(cycleThinkingLevel(string(level), []string{"off", "high", "max"}))
}
func (level ClaudeEffortLevel) Next() ClaudeEffortLevel {
	return ClaudeEffortLevel(cycleThinkingLevel(string(level), []string{"off", "low", "medium", "high"}))
}
func (level MuseSparkThinkingLevel) Next() MuseSparkThinkingLevel {
	return MuseSparkThinkingLevel(cycleThinkingLevel(string(level), []string{"minimal", "low", "medium", "high", "xhigh"}))
}

func (level DeepSeekV4ThinkingLevel) ExtraBody() map[string]any {
	switch level {
	case DeepSeekV4High:
		return map[string]any{"thinking": map[string]any{"type": "enabled"}, "reasoning_effort": "high"}
	case DeepSeekV4Max:
		return map[string]any{"thinking": map[string]any{"type": "enabled"}, "reasoning_effort": "max"}
	default:
		return map[string]any{"thinking": map[string]any{"type": "disabled"}}
	}
}

func (level Qwen35ThinkingLevel) ExtraBody() map[string]any {
	return map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": level == Qwen35On}}
}

func (level Qwen38ThinkingLevel) ExtraBody() map[string]any {
	if level == Qwen38Off {
		return map[string]any{
			"enable_thinking":      false,
			"chat_template_kwargs": map[string]any{"enable_thinking": false},
		}
	}
	return map[string]any{
		"enable_thinking":      true,
		"chat_template_kwargs": map[string]any{"enable_thinking": true},
		"reasoning_effort":     string(level),
	}
}

func (level GlmThinkingLevel) ExtraBody() map[string]any {
	if level == GLMOff {
		return map[string]any{"thinking": map[string]any{"type": "disabled"}}
	}
	return map[string]any{"thinking": map[string]any{"type": "enabled"}}
}

func (level Gpt5ThinkingLevel) ExtraBody() map[string]any {
	effort := string(level)
	if level == GPT5Off {
		effort = "none"
	}
	return map[string]any{"reasoning_effort": effort}
}

func (level Gpt5ThinkingLevel) ThinkingConfig() (map[string]any, bool) {
	if level == GPT5Off {
		return nil, false
	}
	return map[string]any{"summary": "auto", "effort": string(level)}, true
}

func (level MiniMaxThinkingLevel) ExtraBody() map[string]any {
	if level == MiniMaxOff {
		return map[string]any{"thinking": map[string]any{"type": "disabled"}}
	}
	return map[string]any{"thinking": map[string]any{"type": "adaptive"}}
}

func (level ClaudeEffortLevel) ExtraBody() map[string]any {
	switch level {
	case ClaudeLow, ClaudeMedium, ClaudeHigh:
		return map[string]any{"output_config": map[string]any{"effort": string(level)}}
	default:
		return map[string]any{"thinking": map[string]any{"type": "disabled"}}
	}
}

func (level ClaudeEffortLevel) OpenAIExtraBody() map[string]any {
	effort := string(level)
	if level == ClaudeOff {
		effort = "none"
	}
	return map[string]any{"reasoning_effort": effort}
}

func (level MuseSparkThinkingLevel) ExtraBody() map[string]any {
	return map[string]any{"reasoning_effort": string(level)}
}

type thinkingFamily string

const (
	familyDeepSeek  thinkingFamily = "deepseek"
	familyQwen      thinkingFamily = "qwen"
	familyQwen38    thinkingFamily = "qwen38"
	familyGLM       thinkingFamily = "glm"
	familyGPT5      thinkingFamily = "gpt5"
	familyMiniMax   thinkingFamily = "minimax"
	familyClaude    thinkingFamily = "claude"
	familyMuseSpark thinkingFamily = "muse_spark"
)

// ThinkingLevelType is a model-family-tagged thinking selection. Its zero value means no thinking configuration.
type ThinkingLevelType struct {
	family thinkingFamily
	level  string
}

func NoThinking() ThinkingLevelType { return ThinkingLevelType{} }

func DeepSeekV4(level DeepSeekV4ThinkingLevel) ThinkingLevelType {
	return ThinkingLevelType{family: familyDeepSeek, level: string(ParseDeepSeekV4ThinkingLevel(string(level)))}
}
func Qwen35(level Qwen35ThinkingLevel) ThinkingLevelType {
	return ThinkingLevelType{family: familyQwen, level: string(ParseQwen35ThinkingLevel(string(level)))}
}
func Qwen38(level Qwen38ThinkingLevel) ThinkingLevelType {
	return ThinkingLevelType{family: familyQwen38, level: string(ParseQwen38ThinkingLevel(string(level)))}
}
func GLM(level GlmThinkingLevel) ThinkingLevelType {
	return ThinkingLevelType{family: familyGLM, level: string(ParseGlmThinkingLevel(string(level)))}
}
func GPT5(level Gpt5ThinkingLevel) ThinkingLevelType {
	return ThinkingLevelType{family: familyGPT5, level: string(ParseGpt5ThinkingLevel(string(level)))}
}
func MiniMax(level MiniMaxThinkingLevel) ThinkingLevelType {
	return ThinkingLevelType{family: familyMiniMax, level: string(ParseMiniMaxThinkingLevel(string(level)))}
}
func Claude(level ClaudeEffortLevel) ThinkingLevelType {
	return ThinkingLevelType{family: familyClaude, level: string(ParseClaudeEffortLevel(string(level)))}
}
func MuseSpark(level MuseSparkThinkingLevel) ThinkingLevelType {
	return ThinkingLevelType{family: familyMuseSpark, level: string(ParseMuseSparkThinkingLevel(string(level)))}
}

func ParseThinkingLevelType(value string) ThinkingLevelType {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		if value == "none" {
			return NoThinking()
		}
		return NoThinking()
	}
	switch parts[0] {
	case "deepseek":
		return DeepSeekV4(ParseDeepSeekV4ThinkingLevel(parts[1]))
	case "qwen":
		return Qwen35(ParseQwen35ThinkingLevel(parts[1]))
	case "qwen38":
		return Qwen38(ParseQwen38ThinkingLevel(parts[1]))
	case "glm":
		return GLM(ParseGlmThinkingLevel(parts[1]))
	case "gpt5":
		return GPT5(ParseGpt5ThinkingLevel(parts[1]))
	case "minimax":
		return MiniMax(ParseMiniMaxThinkingLevel(parts[1]))
	case "claude":
		return Claude(ParseClaudeEffortLevel(parts[1]))
	case "muse_spark", "muse", "spark":
		return MuseSpark(ParseMuseSparkThinkingLevel(parts[1]))
	default:
		return NoThinking()
	}
}

func (level ThinkingLevelType) String() string {
	if level.family == "" {
		return "none"
	}
	return string(level.family) + ":" + strings.ToLower(level.DisplayName())
}

func (level ThinkingLevelType) Family() string {
	if level.IsNone() {
		return "none"
	}
	return string(level.family)
}

func (level ThinkingLevelType) Level() string     { return level.level }
func (level ThinkingLevelType) IsNone() bool      { return level.family == "" }
func (level ThinkingLevelType) IsSupported() bool { return !level.IsNone() }

func (level ThinkingLevelType) DisplayName() string {
	if level.IsNone() {
		return ""
	}
	return displayThinkingLevel(level.level)
}

func (level ThinkingLevelType) Next() ThinkingLevelType {
	switch level.family {
	case familyDeepSeek:
		return DeepSeekV4(ParseDeepSeekV4ThinkingLevel(cycleThinkingLevel(level.level, []string{"off", "high", "max"})))
	case familyQwen:
		return Qwen35(ParseQwen35ThinkingLevel(cycleThinkingLevel(level.level, []string{"off", "on"})))
	case familyQwen38:
		return Qwen38(ParseQwen38ThinkingLevel(cycleThinkingLevel(level.level, []string{"off", "low", "medium", "xhigh"})))
	case familyGLM:
		return GLM(ParseGlmThinkingLevel(cycleThinkingLevel(level.level, []string{"off", "high", "max"})))
	case familyGPT5:
		return GPT5(ParseGpt5ThinkingLevel(cycleThinkingLevel(level.level, []string{"off", "low", "medium", "high", "xhigh", "max"})))
	case familyMiniMax:
		return MiniMax(ParseMiniMaxThinkingLevel(cycleThinkingLevel(level.level, []string{"off", "high", "max"})))
	case familyClaude:
		return Claude(ParseClaudeEffortLevel(cycleThinkingLevel(level.level, []string{"off", "low", "medium", "high"})))
	case familyMuseSpark:
		return MuseSpark(ParseMuseSparkThinkingLevel(cycleThinkingLevel(level.level, []string{"minimal", "low", "medium", "high", "xhigh"})))
	default:
		return NoThinking()
	}
}

func (level ThinkingLevelType) Effort() (string, bool) {
	switch level.family {
	case familyDeepSeek:
		if level.level == "off" {
			return "", false
		}
		return level.level, true
	case familyQwen:
		return "", false
	case familyQwen38:
		if level.level == "off" {
			return "", false
		}
		return level.level, true
	case familyGLM:
		if level.level == "off" {
			return "", false
		}
		return level.level, true
	case familyGPT5:
		if level.level == "off" {
			return "", false
		}
		return level.level, true
	case familyMiniMax:
		if level.level == "off" {
			return "", false
		}
		return level.level, true
	case familyClaude:
		if level.level == "off" {
			return "", false
		}
		return level.level, true
	case familyMuseSpark:
		return level.level, true
	default:
		return "", false
	}
}

func (level ThinkingLevelType) ExtraBody() (map[string]any, bool) {
	switch level.family {
	case familyDeepSeek:
		return DeepSeekV4ThinkingLevel(level.level).ExtraBody(), true
	case familyQwen:
		return Qwen35ThinkingLevel(level.level).ExtraBody(), true
	case familyQwen38:
		return Qwen38ThinkingLevel(level.level).ExtraBody(), true
	case familyGLM:
		return GlmThinkingLevel(level.level).ExtraBody(), true
	case familyGPT5:
		return Gpt5ThinkingLevel(level.level).ExtraBody(), true
	case familyMiniMax:
		return MiniMaxThinkingLevel(level.level).ExtraBody(), true
	case familyClaude:
		return ClaudeEffortLevel(level.level).ExtraBody(), true
	case familyMuseSpark:
		return MuseSparkThinkingLevel(level.level).ExtraBody(), true
	default:
		return nil, false
	}
}

func (level ThinkingLevelType) ThinkingConfig() (map[string]any, bool) {
	if level.IsNone() {
		return nil, false
	}
	effort, ok := level.Effort()
	if !ok {
		effort = "none"
	}
	if level.family == familyQwen && level.level == "on" {
		effort = "high"
	}
	config := map[string]any{"effort": effort}
	if level.family == familyGPT5 && effort != "none" {
		config["summary"] = "auto"
	}
	return config, true
}

func (level ThinkingLevelType) ExtraBodyForAPI(apiType APIType) (map[string]any, bool) {
	if level.IsNone() {
		return nil, false
	}
	switch apiType {
	case APITypeOpenAI:
		if level.family == familyClaude {
			return ClaudeEffortLevel(level.level).OpenAIExtraBody(), true
		}
		return level.ExtraBody()
	case APITypeOpenAIResponses, APITypeOpenAIWebSocket:
		return level.ThinkingConfig()
	case APITypeAnthropic:
		if level.family == familyQwen && level.level == "on" {
			return nil, false
		}
		if level.family == familyQwen38 && level.level != "off" {
			return nil, false
		}
		if level.family == familyMiniMax {
			if level.level == "off" {
				return map[string]any{"thinking": map[string]any{"type": "disabled"}}, true
			}
			return map[string]any{"thinking": map[string]any{"type": "adaptive"}}, true
		}
		if effort, ok := level.Effort(); ok {
			return map[string]any{"output_config": map[string]any{"effort": effort}}, true
		}
		return map[string]any{"thinking": map[string]any{"type": "disabled"}}, true
	default:
		return nil, false
	}
}

func (level ThinkingLevelType) ThinkingConfigForAPI(apiType APIType) (map[string]any, bool) {
	if apiType == APITypeOpenAIResponses || apiType == APITypeOpenAIWebSocket {
		return level.ThinkingConfig()
	}
	return nil, false
}

func MergeExtraBody(base, overlay map[string]any) map[string]any {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	merged := make(map[string]any, len(base)+len(overlay))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overlay {
		merged[key] = value
	}
	return merged
}

func parseLevel(value string, levels []string, fallback string) string {
	value = normalizeThinkingLevel(value)
	for _, level := range levels {
		if value == level {
			return value
		}
	}
	return fallback
}

func normalizeThinkingLevel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "x_high", "x-high":
		return "xhigh"
	default:
		return value
	}
}

func displayThinkingLevel(value string) string {
	switch value {
	case "xhigh":
		return "XHigh"
	case "on":
		return "On"
	case "off":
		return "Off"
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	case "high":
		return "High"
	case "max":
		return "Max"
	case "minimal":
		return "Minimal"
	default:
		return ""
	}
}

func cycleThinkingLevel(current string, levels []string) string {
	for index, level := range levels {
		if level == current {
			return levels[(index+1)%len(levels)]
		}
	}
	return levels[0]
}
