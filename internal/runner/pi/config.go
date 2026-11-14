package pi

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/misarescu/frankie/internal/runner"
)

// Names the configuration uses.
const (
	// providerID is the pi provider for the LLM gateway.
	providerID = "frankie"
	// keyEnv holds the virtual key; models.json reads it from there.
	keyEnv = "FRANKIE_GATEWAY_KEY"
	// readonlyEnv lists the read-only shell's commands for readonly.ts.
	readonlyEnv = "FRANKIE_READONLY_COMMANDS"
)

// Models is pi's models.json for a session: the gateway as the only custom
// provider, serving only the session's model, with the key taken from the
// environment so it is never written to disk.
func Models(cfg runner.SessionConfig) ([]byte, error) {
	g := cfg.Gateway
	if g.BaseURL == "" {
		return nil, errors.New("pi: the session needs the gateway (StartRequest.gateway)")
	}
	if cfg.Model == "" {
		return nil, errors.New("pi: the session needs a model")
	}
	base := strings.TrimSuffix(g.BaseURL, "/")
	provider := map[string]any{
		"name":   "Frankie LLM gateway",
		"apiKey": "$" + keyEnv,
		"models": []map[string]any{{"id": cfg.Model}},
	}
	switch g.API {
	case runner.ModelAPIAnthropic:
		// The Anthropic client appends /v1/messages. pi sends the key as
		// x-api-key; the gateway also takes it as a bearer token.
		provider["baseUrl"], provider["api"], provider["authHeader"] = base, "anthropic-messages", true
	case runner.ModelAPIOpenAI, "":
		provider["baseUrl"], provider["api"] = base+"/v1", "openai-completions"
	default:
		return nil, errors.New("pi: unknown model API " + string(g.API))
	}
	return json.Marshal(map[string]any{"providers": map[string]any{providerID: provider}})
}

// Args are pi's command-line arguments: RPC mode, no session file, the
// gateway's model, and nothing loaded from the repo or the home directory
// that could add tools, change the prompt or reach other services:
// extensions, skills, prompt templates, themes and MCP servers are off, and
// project-local .pi files are ignored. Context files (AGENTS.md) still load,
// as for the other harnesses.
func Args(cfg runner.SessionConfig, promptFile, extensionFile string) ([]string, error) {
	list, readonly, err := tools(cfg.AllowedTools)
	if err != nil {
		return nil, err
	}
	if readonly != (extensionFile != "") {
		return nil, errors.New("pi: the read-only shell needs its extension, and only it")
	}
	args := []string{
		"--mode", "rpc", "--no-session",
		"--provider", providerID, "--model", cfg.Model,
		"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-mcp",
		"--no-approve", "--offline",
	}
	if len(list) > 0 {
		args = append(args, "--tools", strings.Join(list, ","))
	}
	if extensionFile != "" {
		// Explicit extension paths load despite --no-extensions.
		args = append(args, "--extension", extensionFile)
	}
	if promptFile != "" {
		// A path appends the file's contents.
		args = append(args, "--append-system-prompt", promptFile)
	}
	return args, nil
}

// environment is what pi needs besides its arguments: its agent directory
// (models.json, outside the workspace), the key, the read-only command list,
// and no network activity beyond the model calls.
func environment(cfg runner.SessionConfig, agentDir string) (map[string]string, error) {
	commands, err := json.Marshal(runner.ReadOnlyCommands)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"PI_CODING_AGENT_DIR":   agentDir,
		keyEnv:                  cfg.Gateway.APIKey,
		readonlyEnv:             string(commands),
		"PI_OFFLINE":            "1",
		"PI_SKIP_VERSION_CHECK": "1",
		"PI_TELEMETRY":          "0",
	}, nil
}
