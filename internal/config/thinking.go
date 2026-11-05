package config

import (
	"strings"

	"agentsd/internal/llm"
)

type ThinkingMatcher struct{}

func (ThinkingMatcher) MatchForModel(modelID string) llm.ThinkingLevelType {
	id := strings.ToLower(modelID)
	switch {
	case strings.Contains(id, "deepseek") && (strings.Contains(id, "4") || strings.Contains(id, "flash")):
		return llm.DeepSeekV4(llm.DeepSeekV4High)
	case isQwen38(id):
		return llm.Qwen38(llm.Qwen38XHigh)
	case strings.Contains(id, "qwen") && strings.Contains(id, "3."):
		return llm.Qwen35(llm.Qwen35On)
	case strings.Contains(id, "glm"):
		return llm.GLM(llm.GLMHigh)
	case matchesGPTGeneration(id, '5') || matchesGPTGeneration(id, '6'):
		return llm.GPT5(llm.GPT5Medium)
	case strings.Contains(id, "minimax") && strings.Contains(id, "m3"):
		return llm.MiniMax(llm.MiniMaxHigh)
	case strings.Contains(id, "muse") || strings.Contains(id, "spark"):
		return llm.MuseSpark(llm.MuseSparkMedium)
	case strings.Contains(id, "claude"):
		return llm.Claude(llm.ClaudeHigh)
	default:
		return llm.NoThinking()
	}
}

func (ThinkingMatcher) SupportedLevels(modelID string) []llm.ThinkingLevelType {
	id := strings.ToLower(modelID)
	switch {
	case strings.Contains(id, "deepseek") && (strings.Contains(id, "4") || strings.Contains(id, "flash")):
		return []llm.ThinkingLevelType{
			llm.DeepSeekV4(llm.DeepSeekV4Off),
			llm.DeepSeekV4(llm.DeepSeekV4High),
			llm.DeepSeekV4(llm.DeepSeekV4Max),
		}
	case isQwen38(id):
		return []llm.ThinkingLevelType{
			llm.Qwen38(llm.Qwen38Off),
			llm.Qwen38(llm.Qwen38Low),
			llm.Qwen38(llm.Qwen38Medium),
			llm.Qwen38(llm.Qwen38XHigh),
		}
	case strings.Contains(id, "qwen") && strings.Contains(id, "3."):
		return []llm.ThinkingLevelType{llm.Qwen35(llm.Qwen35Off), llm.Qwen35(llm.Qwen35On)}
	case strings.Contains(id, "glm"):
		return []llm.ThinkingLevelType{
			llm.GLM(llm.GLMOff),
			llm.GLM(llm.GLMHigh),
			llm.GLM(llm.GLMMax),
		}
	case matchesGPTGeneration(id, '6'):
		return []llm.ThinkingLevelType{
			llm.GPT5(llm.GPT5Low),
			llm.GPT5(llm.GPT5Medium),
			llm.GPT5(llm.GPT5High),
			llm.GPT5(llm.GPT5XHigh),
			llm.GPT5(llm.GPT5Max),
		}
	case matchesGPTGeneration(id, '5') && (strings.Contains(id, "5.6") || strings.Contains(id, "5-6")):
		return []llm.ThinkingLevelType{
			llm.GPT5(llm.GPT5Off),
			llm.GPT5(llm.GPT5Low),
			llm.GPT5(llm.GPT5Medium),
			llm.GPT5(llm.GPT5High),
			llm.GPT5(llm.GPT5XHigh),
			llm.GPT5(llm.GPT5Max),
		}
	case matchesGPTGeneration(id, '5'):
		return []llm.ThinkingLevelType{
			llm.GPT5(llm.GPT5Off),
			llm.GPT5(llm.GPT5Low),
			llm.GPT5(llm.GPT5Medium),
			llm.GPT5(llm.GPT5High),
			llm.GPT5(llm.GPT5XHigh),
		}
	case (strings.Contains(id, "claude") && strings.Contains(id, "fable")) ||
		(strings.Contains(id, "claude") && strings.Contains(id, "mythos")):
		return []llm.ThinkingLevelType{
			llm.Claude(llm.ClaudeLow),
			llm.Claude(llm.ClaudeMedium),
			llm.Claude(llm.ClaudeHigh),
		}
	case strings.Contains(id, "claude") && strings.Contains(id, "5"):
		return []llm.ThinkingLevelType{
			llm.Claude(llm.ClaudeOff),
			llm.Claude(llm.ClaudeLow),
			llm.Claude(llm.ClaudeMedium),
			llm.Claude(llm.ClaudeHigh),
		}
	case strings.Contains(id, "muse") || strings.Contains(id, "spark"):
		return []llm.ThinkingLevelType{
			llm.MuseSpark(llm.MuseSparkMinimal),
			llm.MuseSpark(llm.MuseSparkLow),
			llm.MuseSpark(llm.MuseSparkMedium),
			llm.MuseSpark(llm.MuseSparkHigh),
			llm.MuseSpark(llm.MuseSparkXHigh),
		}
	default:
		return nil
	}
}

func (matcher ThinkingMatcher) CoerceSaved(saved, modelID string) llm.ThinkingLevelType {
	parsed := llm.ParseThinkingLevelType(saved)
	defaultLevel := matcher.MatchForModel(modelID)
	supported := matcher.SupportedLevels(modelID)
	if parsed.Family() == defaultLevel.Family() && (len(supported) == 0 || containsThinkingLevel(supported, parsed)) {
		return parsed
	}
	return defaultLevel
}

func isQwen38(id string) bool {
	return strings.Contains(id, "qwen") && (strings.Contains(id, "3.8") || strings.Contains(id, "3-8"))
}

func matchesGPTGeneration(id string, generation byte) bool {
	for offset := 0; offset < len(id); {
		index := strings.Index(id[offset:], "gpt")
		if index < 0 {
			return false
		}
		index += offset
		if index > 0 && isASCIIAlphaNumeric(id[index-1]) {
			offset = index + 3
			continue
		}
		suffix := id[index+3:]
		first := 0
		for first < len(suffix) && !isASCIIAlphaNumeric(suffix[first]) {
			first++
		}
		if first < len(suffix) && suffix[first] == generation {
			return true
		}
		offset = index + 3
	}
	return false
}

func isASCIIAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func containsThinkingLevel(levels []llm.ThinkingLevelType, target llm.ThinkingLevelType) bool {
	for _, level := range levels {
		if level == target {
			return true
		}
	}
	return false
}
