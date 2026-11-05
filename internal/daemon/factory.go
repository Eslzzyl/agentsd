package daemon

import (
	"context"
	"fmt"
	"os"

	"agentsd/internal/agent"
	"agentsd/internal/config"
	"agentsd/internal/llm"
	"agentsd/internal/llm/openai"
	"agentsd/internal/tools"
)

type ConfigFactory struct {
	ConfigPath string
	AuthPath   string
}

func (factory ConfigFactory) New(model string) (Runner, string, error) {
	cfg, err := config.Load(factory.ConfigPath)
	if err != nil {
		return nil, "", err
	}
	if model != "" {
		cfg.Model = model
	}
	resolved, err := cfg.Resolve()
	if err != nil {
		return nil, "", err
	}
	if resolved.APIType != llm.APITypeOpenAI {
		return nil, "", fmt.Errorf("daemon: API type %q is not supported by the initial daemon", resolved.APIType)
	}
	auth, err := config.LoadAuth(factory.AuthPath)
	if err != nil {
		return nil, "", err
	}
	client, err := openai.NewClient(openai.Config{
		APIKey:  auth.APIKey(resolved.ProviderID),
		BaseURL: resolved.BaseURL,
	})
	if err != nil {
		return nil, "", err
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, "", fmt.Errorf("daemon: determine working directory: %w", err)
	}
	registry, err := tools.NewRegistry(tools.ShellTool{WorkingDir: workingDir})
	if err != nil {
		return nil, "", err
	}
	maxTokens := resolved.MaxOutputTokens
	var maxTokensPtr *int
	if maxTokens > 0 {
		maxTokensPtr = &maxTokens
	}
	runtime, err := agent.NewRuntime(client, registry, agent.RuntimeOptions{})
	if err != nil {
		return nil, "", err
	}
	return configuredRunner{
		runtime:   runtime,
		model:     resolved.RequestModelID,
		maxTokens: maxTokensPtr,
		extraBody: resolved.MergedExtraBody(),
	}, resolved.RequestModelID, nil
}

type configuredRunner struct {
	runtime   *agent.Runtime
	model     string
	maxTokens *int
	extraBody map[string]any
}

func (runner configuredRunner) Run(ctx context.Context, request agent.RunRequest) (agent.RunResult, error) {
	request.Model = runner.model
	request.MaxTokens = runner.maxTokens
	request.ExtraBody = runner.extraBody
	return runner.runtime.Run(ctx, request)
}
