package opencode

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/misarescu/frankie/internal/runner"
)

// Names the configuration uses.
const (
	// providerID is the opencode provider for the LLM gateway.
	providerID = "frankie"
	// keyEnv holds the virtual key; the provider reads it from there.
	keyEnv = "FRANKIE_GATEWAY_KEY"
	// agentID is opencode's default primary agent, which sessions run.
	agentID = "build"
)

// Provider packages built into opencode, by the API the gateway serves.
var packages = map[runner.ModelAPI]string{
	runner.ModelAPIAnthropic: "@opencode/ai/providers/anthropic-compatible",
	runner.ModelAPIOpenAI:    "@opencode/ai/providers/openai-compatible",
}

// Config is the opencode configuration of a session, passed in
// OPENCODE_CONFIG_CONTENT: the gateway as the only provider, serving only the
// session's model, the role prompt as an instructions file (appended to
// opencode's own system prompt) and the role's tools as the agent's
// permissions.
func Config(cfg runner.SessionConfig, promptFile string) ([]byte, error) {
	g := cfg.Gateway
	if g.BaseURL == "" {
		return nil, errors.New("opencode: the session needs the gateway (StartRequest.gateway)")
	}
	if cfg.Model == "" {
		return nil, errors.New("opencode: the session needs a model")
	}
	api := g.API
	if api == "" {
		api = runner.ModelAPIOpenAI
	}
	pkg, ok := packages[api]
	if !ok {
		return nil, errors.New("opencode: unknown model API " + string(api))
	}
	rules, err := permissions(cfg.AllowedTools)
	if err != nil {
		return nil, err
	}
	agent := map[string]any{"permissions": rules}
	if cfg.MaxTurns > 0 {
		agent["steps"] = cfg.MaxTurns
	}
	c := map[string]any{
		"providers": map[string]any{
			providerID: map[string]any{
				"name":    "Frankie LLM gateway",
				"env":     []string{keyEnv},
				"package": pkg,
				// Both packages append their API path (/chat/completions, /messages) to a /v1 base.
				"settings": map[string]any{"baseURL": strings.TrimSuffix(g.BaseURL, "/") + "/v1"},
				"models":   map[string]any{cfg.Model: map[string]any{"name": cfg.Model}},
			},
		},
		"model":  providerID + "/" + cfg.Model,
		"update": "disable",
		"share":  "disabled",
		"agents": map[string]any{agentID: agent},
	}
	if promptFile != "" {
		c["instructions"] = []string{promptFile}
	}
	return json.Marshal(c)
}

// environment is what opencode needs besides the configuration: no update
// checks, no model catalog download, no free built-in provider, and no
// project configuration from the repo, which the agent controls and which
// could loosen the role's permissions.
func environment(cfg runner.SessionConfig, config []byte, password string) map[string]string {
	return map[string]string{
		"OPENCODE_CONFIG_CONTENT":         string(config),
		"OPENCODE_SERVER_PASSWORD":        password,
		"OPENCODE_DISABLE_AUTOUPDATE":     "1",
		"OPENCODE_DISABLE_MODELS_FETCH":   "1",
		"OPENCODE_DISABLE_FREE_MODELS":    "1",
		"OPENCODE_DISABLE_PROJECT_CONFIG": "1",
		keyEnv:                            cfg.Gateway.APIKey,
	}
}
