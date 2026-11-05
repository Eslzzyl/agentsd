package config

import (
	"reflect"
	"strings"
	"testing"

	"agentsd/internal/llm"
)

const sampleConfig = `
provider = "gateway"
model = "gpt-6-luna"
thinking_level = "gpt5:high"

[providers.gateway]
api_type = "openai"
base_url = "https://api.example.test/v1"

[providers.gateway.models.gpt-6-luna]
api_type = "openai_responses"
base_url = "https://responses.example.test/v1"
request_model_id = "gpt-6.1-luna"
context_window = 272000
max_output_tokens = 128000
supports_images = true
supports_streaming = true

[providers.gateway.models.gpt-4o-mini]
request_model_id = "gpt-4o-mini"
`

func TestParseTOMLAndResolveSelectedModel(t *testing.T) {
	cfg, err := ParseTOML([]byte(sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want := ResolvedModel{
		ProviderID:        "gateway",
		ModelID:           "gpt-6-luna",
		RequestModelID:    "gpt-6.1-luna",
		APIType:           llm.APITypeOpenAIResponses,
		BaseURL:           "https://responses.example.test/v1",
		ThinkingLevel:     llm.GPT5(llm.GPT5High),
		ContextWindow:     272000,
		MaxOutputTokens:   128000,
		SupportsImages:    true,
		SupportsStreaming: true,
	}
	if !reflect.DeepEqual(resolved, want) {
		t.Fatalf("resolved model = %#v, want %#v", resolved, want)
	}
}

func TestResolveInheritsProviderDefaultsAndModelName(t *testing.T) {
	cfg, err := ParseTOML([]byte(`
provider = "hosted"
model = "qwen-3-8-max"

[providers.hosted]
api_type = "openai"
base_url = "https://api.example.test/v1"

[providers.hosted.models.qwen-3-8-max]
`))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.APIType != llm.APITypeOpenAI || resolved.BaseURL != "https://api.example.test/v1" {
		t.Fatalf("provider defaults = %#v", resolved)
	}
	if resolved.RequestModelID != "qwen-3-8-max" || resolved.ThinkingLevel != llm.Qwen38(llm.Qwen38XHigh) {
		t.Fatalf("model defaults = %#v", resolved)
	}
}

func TestResolveFallsBackFromIncompatibleThinkingFamily(t *testing.T) {
	cfg, err := ParseTOML([]byte(strings.Replace(sampleConfig, `"gpt5:high"`, `"claude:high"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ThinkingLevel != llm.GPT5(llm.GPT5Medium) {
		t.Fatalf("thinking level = %q, want model default", resolved.ThinkingLevel)
	}
}

func TestParseTOMLRejectsUnknownFieldsAndAPIType(t *testing.T) {
	unknownField := strings.Replace(sampleConfig, `model = "gpt-6-luna"`, "model = \"gpt-6-luna\"\nextra = true", 1)
	if _, err := ParseTOML([]byte(unknownField)); err == nil {
		t.Fatal("unknown top-level field was accepted")
	}
	unknownAPI := strings.Replace(sampleConfig, `api_type = "openai"`, `api_type = "openai_chat_completions"`, 1)
	if _, err := ParseTOML([]byte(unknownAPI)); err == nil {
		t.Fatal("unknown API type was accepted")
	}
}

func TestParseTOMLRequiresSelectedProviderAndModel(t *testing.T) {
	cfg, err := ParseTOML([]byte(sampleConfig))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Model = "missing"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("validation error = %v, want missing model error", err)
	}
}

func TestResolvedModelMergesModelExtraBodyAndThinking(t *testing.T) {
	cfg, err := ParseTOML([]byte(`
provider = "hosted"
model = "gpt-5.6"
thinking_level = "gpt5:high"

[providers.hosted]
api_type = "openai"
base_url = "https://api.example.test/v1"

[providers.hosted.models."gpt-5.6".extra_body]
custom_flag = true
reasoning_effort = "low"
`))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := cfg.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	body := resolved.MergedExtraBody()
	if body["custom_flag"] != true || body["reasoning_effort"] != "high" {
		t.Fatalf("merged request body = %#v", body)
	}
}
