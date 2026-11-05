package llm

import (
	"reflect"
	"testing"
)

func TestThinkingLevelTypeParsingAndCycling(t *testing.T) {
	tests := []struct {
		value string
		want  ThinkingLevelType
	}{
		{"deepseek:HIGH", DeepSeekV4(DeepSeekV4High)},
		{"qwen:on", Qwen35(Qwen35On)},
		{"qwen38:x_high", Qwen38(Qwen38XHigh)},
		{"glm:unknown", GLM(GLMOff)},
		{"gpt5:max", GPT5(GPT5Max)},
		{"minimax:high", MiniMax(MiniMaxHigh)},
		{"claude:medium", Claude(ClaudeMedium)},
		{"muse:xhigh", MuseSpark(MuseSparkXHigh)},
		{"not-a-family:high", NoThinking()},
		{"none", NoThinking()},
	}
	for _, test := range tests {
		if got := ParseThinkingLevelType(test.value); got != test.want {
			t.Errorf("ParseThinkingLevelType(%q) = %q, want %q", test.value, got, test.want)
		}
	}
	if got := DeepSeekV4(DeepSeekV4Max).Next(); got != DeepSeekV4(DeepSeekV4Off) {
		t.Fatalf("DeepSeek cycle = %q, want off", got)
	}
	if got := Qwen35(Qwen35Off).Next(); got != Qwen35(Qwen35On) {
		t.Fatalf("Qwen cycle = %q, want on", got)
	}
	if got := MuseSpark(MuseSparkXHigh).Next(); got != MuseSpark(MuseSparkMinimal) {
		t.Fatalf("MuseSpark cycle = %q, want minimal", got)
	}
}

func TestThinkingPayloadsByAPI(t *testing.T) {
	tests := []struct {
		name    string
		level   ThinkingLevelType
		apiType APIType
		want    map[string]any
		ok      bool
	}{
		{"deepseek enabled", DeepSeekV4(DeepSeekV4High), APITypeOpenAI, map[string]any{"thinking": map[string]any{"type": "enabled"}, "reasoning_effort": "high"}, true},
		{"deepseek disabled", DeepSeekV4(DeepSeekV4Off), APITypeOpenAI, map[string]any{"thinking": map[string]any{"type": "disabled"}}, true},
		{"qwen 3.5 enabled", Qwen35(Qwen35On), APITypeOpenAI, map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": true}}, true},
		{"qwen 3.5 responses", Qwen35(Qwen35On), APITypeOpenAIResponses, map[string]any{"effort": "high"}, true},
		{"qwen 3.5 anthropic unsupported", Qwen35(Qwen35On), APITypeAnthropic, nil, false},
		{"qwen 3.8 xhigh", Qwen38(Qwen38XHigh), APITypeOpenAI, map[string]any{"enable_thinking": true, "chat_template_kwargs": map[string]any{"enable_thinking": true}, "reasoning_effort": "xhigh"}, true},
		{"glm max", GLM(GLMMax), APITypeOpenAI, map[string]any{"thinking": map[string]any{"type": "enabled"}}, true},
		{"gpt chat", GPT5(GPT5High), APITypeOpenAI, map[string]any{"reasoning_effort": "high"}, true},
		{"gpt responses", GPT5(GPT5High), APITypeOpenAIResponses, map[string]any{"summary": "auto", "effort": "high"}, true},
		{"gpt off responses", GPT5(GPT5Off), APITypeOpenAIResponses, map[string]any{"effort": "none"}, true},
		{"minimax adaptive", MiniMax(MiniMaxHigh), APITypeOpenAI, map[string]any{"thinking": map[string]any{"type": "adaptive"}}, true},
		{"claude chat translation", Claude(ClaudeHigh), APITypeOpenAI, map[string]any{"reasoning_effort": "high"}, true},
		{"claude native", Claude(ClaudeHigh), APITypeAnthropic, map[string]any{"output_config": map[string]any{"effort": "high"}}, true},
		{"claude disabled", Claude(ClaudeOff), APITypeAnthropic, map[string]any{"thinking": map[string]any{"type": "disabled"}}, true},
		{"muse spark", MuseSpark(MuseSparkMedium), APITypeOpenAI, map[string]any{"reasoning_effort": "medium"}, true},
		{"google unsupported", GPT5(GPT5High), APITypeGoogle, nil, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := test.level.ExtraBodyForAPI(test.apiType)
			if ok != test.ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ExtraBodyForAPI() = (%#v, %t), want (%#v, %t)", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestMergeExtraBodyThinkingOverridesProviderValue(t *testing.T) {
	base := map[string]any{"custom": "kept", "reasoning_effort": "low"}
	thinking := map[string]any{"reasoning_effort": "high"}
	got := MergeExtraBody(base, thinking)
	want := map[string]any{"custom": "kept", "reasoning_effort": "high"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged body = %#v, want %#v", got, want)
	}
	if base["reasoning_effort"] != "low" {
		t.Fatalf("merge mutated base map: %#v", base)
	}
}
