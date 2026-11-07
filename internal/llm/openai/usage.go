package openai

import "agentsd/internal/llm"

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	PromptDetails    *struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
}

func normalizeUsage(raw chatUsage) llm.Usage {
	cacheRead := 0
	cacheWrite := 0
	if raw.PromptDetails != nil {
		cacheRead = raw.PromptDetails.CachedTokens
		cacheWrite = raw.PromptDetails.CacheWriteTokens
	}
	input := raw.PromptTokens - cacheRead - cacheWrite
	if input < 0 {
		input = 0
	}
	total := raw.TotalTokens
	if total == 0 {
		total = input + cacheRead + cacheWrite + raw.CompletionTokens
	}
	return llm.Usage{
		Input:      input,
		Output:     raw.CompletionTokens,
		CacheRead:  cacheRead,
		CacheWrite: cacheWrite,
		Total:      total,
	}
}
