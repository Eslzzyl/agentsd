package config

import (
	"testing"

	"agentsd/internal/llm"
)

func TestThinkingMatcherDefaults(t *testing.T) {
	matcher := ThinkingMatcher{}
	tests := []struct {
		model string
		want  llm.ThinkingLevelType
	}{
		{"deepseek-v4", llm.DeepSeekV4(llm.DeepSeekV4High)},
		{"deepseek-v4-flash", llm.DeepSeekV4(llm.DeepSeekV4High)},
		{"qwen-3.5-plus", llm.Qwen35(llm.Qwen35On)},
		{"qwen-3.8-plus", llm.Qwen38(llm.Qwen38XHigh)},
		{"glm-4.7", llm.GLM(llm.GLMHigh)},
		{"gpt-5.4", llm.GPT5(llm.GPT5Medium)},
		{"gpt-5-6-luna", llm.GPT5(llm.GPT5Medium)},
		{"gpt-6-astra", llm.GPT5(llm.GPT5Medium)},
		{"minimax-m3", llm.MiniMax(llm.MiniMaxHigh)},
		{"muse-spark", llm.MuseSpark(llm.MuseSparkMedium)},
		{"claude-5-sonnet", llm.Claude(llm.ClaudeHigh)},
		{"gpt-4o-mini", llm.NoThinking()},
		{"other-model", llm.NoThinking()},
	}
	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			if got := matcher.MatchForModel(test.model); got != test.want {
				t.Fatalf("MatchForModel(%q) = %q, want %q", test.model, got, test.want)
			}
		})
	}
}

func TestThinkingMatcherSupportedLevels(t *testing.T) {
	matcher := ThinkingMatcher{}
	tests := []struct {
		model string
		want  []llm.ThinkingLevelType
	}{
		{"deepseek-v4", []llm.ThinkingLevelType{llm.DeepSeekV4(llm.DeepSeekV4Off), llm.DeepSeekV4(llm.DeepSeekV4High), llm.DeepSeekV4(llm.DeepSeekV4Max)}},
		{"qwen-3.5", []llm.ThinkingLevelType{llm.Qwen35(llm.Qwen35Off), llm.Qwen35(llm.Qwen35On)}},
		{"qwen-3.8", []llm.ThinkingLevelType{llm.Qwen38(llm.Qwen38Off), llm.Qwen38(llm.Qwen38Low), llm.Qwen38(llm.Qwen38Medium), llm.Qwen38(llm.Qwen38XHigh)}},
		{"glm-4", []llm.ThinkingLevelType{llm.GLM(llm.GLMOff), llm.GLM(llm.GLMHigh), llm.GLM(llm.GLMMax)}},
		{"gpt-6", []llm.ThinkingLevelType{llm.GPT5(llm.GPT5Low), llm.GPT5(llm.GPT5Medium), llm.GPT5(llm.GPT5High), llm.GPT5(llm.GPT5XHigh), llm.GPT5(llm.GPT5Max)}},
		{"gpt-5.6", []llm.ThinkingLevelType{llm.GPT5(llm.GPT5Off), llm.GPT5(llm.GPT5Low), llm.GPT5(llm.GPT5Medium), llm.GPT5(llm.GPT5High), llm.GPT5(llm.GPT5XHigh), llm.GPT5(llm.GPT5Max)}},
		{"gpt-5.4", []llm.ThinkingLevelType{llm.GPT5(llm.GPT5Off), llm.GPT5(llm.GPT5Low), llm.GPT5(llm.GPT5Medium), llm.GPT5(llm.GPT5High), llm.GPT5(llm.GPT5XHigh)}},
		{"claude-fable", []llm.ThinkingLevelType{llm.Claude(llm.ClaudeLow), llm.Claude(llm.ClaudeMedium), llm.Claude(llm.ClaudeHigh)}},
		{"claude-5-sonnet", []llm.ThinkingLevelType{llm.Claude(llm.ClaudeOff), llm.Claude(llm.ClaudeLow), llm.Claude(llm.ClaudeMedium), llm.Claude(llm.ClaudeHigh)}},
		{"muse-spark", []llm.ThinkingLevelType{llm.MuseSpark(llm.MuseSparkMinimal), llm.MuseSpark(llm.MuseSparkLow), llm.MuseSpark(llm.MuseSparkMedium), llm.MuseSpark(llm.MuseSparkHigh), llm.MuseSpark(llm.MuseSparkXHigh)}},
		{"gpt-4o", nil},
	}
	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			got := matcher.SupportedLevels(test.model)
			if len(got) != len(test.want) {
				t.Fatalf("SupportedLevels(%q) length = %d, want %d (%v)", test.model, len(got), len(test.want), got)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("SupportedLevels(%q)[%d] = %q, want %q", test.model, index, got[index], test.want[index])
				}
			}
		})
	}
}

func TestThinkingMatcherCoercesSavedSelection(t *testing.T) {
	matcher := ThinkingMatcher{}
	tests := []struct {
		name  string
		saved string
		model string
		want  llm.ThinkingLevelType
	}{
		{"retains supported selection", "gpt5:high", "gpt-6-astra", llm.GPT5(llm.GPT5High)},
		{"uses model default for mismatched family", "claude:high", "gpt-6-astra", llm.GPT5(llm.GPT5Medium)},
		{"uses model default for unsupported level", "gpt5:max", "gpt-5.4", llm.GPT5(llm.GPT5Medium)},
		{"uses new family default after model change", "qwen:on", "qwen-3.8", llm.Qwen38(llm.Qwen38XHigh)},
		{"falls back for malformed value", "unknown:value", "qwen-3.5", llm.Qwen35(llm.Qwen35On)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := matcher.CoerceSaved(test.saved, test.model); got != test.want {
				t.Fatalf("CoerceSaved(%q, %q) = %q, want %q", test.saved, test.model, got, test.want)
			}
		})
	}
}
