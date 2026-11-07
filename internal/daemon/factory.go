package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"agentsd/internal/agent"
	"agentsd/internal/config"
	"agentsd/internal/llm"
	"agentsd/internal/llm/openai"
	"agentsd/internal/tools"
	"agentsd/internal/web"
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
	workspace, err := tools.NewWorkspace(workingDir)
	if err != nil {
		return nil, "", err
	}
	artifactRoot := filepath.Join(filepath.Dir(factory.ConfigPath), "artifacts")
	artifactStore, err := tools.NewArtifactStore(artifactRoot)
	if err != nil {
		return nil, "", err
	}
	limiter := tools.OutputLimiter{Store: artifactStore}
	webRuntime := web.NewRuntime(cfg.Search, cfg.Fetch, auth, web.ModelContext{
		ProviderID:     resolved.ProviderID,
		ModelID:        resolved.ModelID,
		RequestModelID: resolved.RequestModelID,
		APIType:        string(resolved.APIType),
		BaseURL:        resolved.BaseURL,
		ExtraBody:      resolved.MergedExtraBody(),
	}, nil)
	searchClient := web.NewSearchClient(webRuntime)
	registry, err := tools.NewRegistry(
		tools.ReadTool{Workspace: workspace, Limiter: limiter},
		tools.GlobTool{Workspace: workspace, Limiter: limiter},
		tools.GrepTool{Workspace: workspace, Limiter: limiter},
		tools.FindTool{Workspace: workspace, Limiter: limiter},
		tools.WriteTool{Workspace: workspace},
		tools.EditTool{Workspace: workspace},
		tools.ShellTool{WorkingDir: workspace.Root, MaxOutputBytes: tools.DefaultShellMaxOutputBytes},
		tools.WaitTool{},
		tools.WebSearchTool{Client: searchClient, Limiter: limiter},
		tools.WebFetchTool{Runtime: webRuntime, Limiter: limiter},
	)
	if err != nil {
		return nil, "", err
	}
	maxTokens := resolved.MaxOutputTokens
	var maxTokensPtr *int
	if maxTokens > 0 {
		maxTokensPtr = &maxTokens
	}
	runtime, err := agent.NewRuntime(client, registry, agent.RuntimeOptions{
		ContextWindow: resolved.ContextWindow,
		Session:       tools.SessionContext{CWD: workspace.Root, WorkspaceRoot: workspace.Root},
	})
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

func (runner configuredRunner) UsesMessageQueue() bool {
	return true
}

func (runner configuredRunner) Compact(ctx context.Context, request agent.RunRequest) (agent.RunResult, agent.ControlResult, error) {
	request.Model = runner.model
	request.MaxTokens = runner.maxTokens
	request.ExtraBody = runner.extraBody
	return runner.runtime.Compact(ctx, request)
}
