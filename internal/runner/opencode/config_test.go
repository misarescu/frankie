package opencode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/runner"
)

func TestConfigPointsTheOnlyProviderAtTheGateway(t *testing.T) {
	tests := []struct {
		api     runner.ModelAPI
		wantPkg string
	}{
		{runner.ModelAPIOpenAI, "@opencode/ai/providers/openai-compatible"},
		{runner.ModelAPIAnthropic, "@opencode/ai/providers/anthropic-compatible"},
		{"", "@opencode/ai/providers/openai-compatible"},
	}
	for _, tt := range tests {
		raw, err := Config(runner.SessionConfig{Model: "openai/gpt-x", MaxTurns: 9,
			Gateway: runner.Gateway{BaseURL: "http://gw:4000/", APIKey: "sk-v", API: tt.api}}, "/tmp/role.md")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "sk-v") {
			t.Fatal("the key is in the configuration; it belongs in the environment")
		}
		var c struct {
			Providers map[string]struct {
				Env      []string                   `json:"env"`
				Package  string                     `json:"package"`
				Settings map[string]string          `json:"settings"`
				Models   map[string]json.RawMessage `json:"models"`
			} `json:"providers"`
			Model        string   `json:"model"`
			Instructions []string `json:"instructions"`
			Agents       map[string]struct {
				Steps       int    `json:"steps"`
				Permissions []rule `json:"permissions"`
			} `json:"agents"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		p, ok := c.Providers[providerID]
		if len(c.Providers) != 1 || !ok {
			t.Fatalf("providers = %v", c.Providers)
		}
		if p.Package != tt.wantPkg || p.Settings["baseURL"] != "http://gw:4000/v1" || len(p.Env) != 1 || p.Env[0] != keyEnv {
			t.Errorf("%s: provider = %+v", tt.api, p)
		}
		if _, ok := p.Models["openai/gpt-x"]; !ok || len(p.Models) != 1 || c.Model != "frankie/openai/gpt-x" {
			t.Errorf("models = %v, model = %q", p.Models, c.Model)
		}
		if len(c.Instructions) != 1 || c.Instructions[0] != "/tmp/role.md" {
			t.Errorf("instructions = %v", c.Instructions)
		}
		if a := c.Agents[agentID]; a.Steps != 9 || len(a.Permissions) == 0 {
			t.Errorf("agent = %+v", a)
		}
	}
}

func TestConfigErrors(t *testing.T) {
	gw := runner.Gateway{BaseURL: "http://gw", APIKey: "k"}
	for name, cfg := range map[string]runner.SessionConfig{
		"no gateway":   {Model: "m"},
		"no model":     {Gateway: gw},
		"unknown API":  {Model: "m", Gateway: runner.Gateway{BaseURL: "http://gw", API: "grpc"}},
		"unknown tool": {Model: "m", Gateway: gw, AllowedTools: []string{"teleport"}},
	} {
		if _, err := Config(cfg, ""); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEnvironmentLocksDownOpencode(t *testing.T) {
	env := environment(runner.SessionConfig{Gateway: runner.Gateway{APIKey: "sk-v"}}, []byte(`{}`), "pw")
	for k, want := range map[string]string{
		keyEnv: "sk-v", "OPENCODE_SERVER_PASSWORD": "pw", "OPENCODE_CONFIG_CONTENT": "{}",
		"OPENCODE_DISABLE_PROJECT_CONFIG": "1", "OPENCODE_DISABLE_FREE_MODELS": "1",
		"OPENCODE_DISABLE_MODELS_FETCH": "1", "OPENCODE_DISABLE_AUTOUPDATE": "1",
	} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
}

// D54: the session's MCP servers are remote servers with a long execution
// timeout, and their tools are allowed whatever the role's tools.
func TestConfigMCP(t *testing.T) {
	raw, err := Config(runner.SessionConfig{Model: "m", Gateway: runner.Gateway{BaseURL: "http://gw"}, AllowedTools: []string{"read"},
		MCPServers: []runner.MCPServer{{Name: "frankie", URL: "http://127.0.0.1:9000", Tools: []string{"ask_human"}}}}, "")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		MCP struct {
			Servers map[string]struct {
				Type    string           `json:"type"`
				URL     string           `json:"url"`
				Timeout map[string]int64 `json:"timeout"`
			} `json:"servers"`
		} `json:"mcp"`
		Agents map[string]struct {
			Permissions []rule `json:"permissions"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	s := c.MCP.Servers["frankie"]
	if s.Type != "remote" || s.URL != "http://127.0.0.1:9000" || s.Timeout["execution"] != runner.MCPToolTimeout.Milliseconds() {
		t.Errorf("server = %+v", s)
	}
	rules := c.Agents[agentID].Permissions
	if rules[len(rules)-1] != (rule{"frankie_ask_human", "*", "allow"}) {
		t.Errorf("rules = %v", rules)
	}
	none, _ := Config(runner.SessionConfig{Model: "m", Gateway: runner.Gateway{BaseURL: "http://gw"}}, "")
	if strings.Contains(string(none), `"mcp"`) {
		t.Errorf("MCP config without servers: %s", none)
	}
}
