package pi

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/runner"
)

func TestModelsPointsTheOnlyProviderAtTheGateway(t *testing.T) {
	tests := []struct {
		api               runner.ModelAPI
		wantBase, wantAPI string
		wantAuthHeader    bool
	}{
		{runner.ModelAPIOpenAI, "http://gw:4000/v1", "openai-completions", false},
		{"", "http://gw:4000/v1", "openai-completions", false},
		{runner.ModelAPIAnthropic, "http://gw:4000", "anthropic-messages", true},
	}
	for _, tt := range tests {
		raw, err := Models(runner.SessionConfig{Model: "openai/gpt-x", Gateway: runner.Gateway{BaseURL: "http://gw:4000/", APIKey: "sk-v", API: tt.api}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "sk-v") {
			t.Fatal("the key is in models.json; it belongs in the environment")
		}
		var m struct {
			Providers map[string]struct {
				BaseURL    string `json:"baseUrl"`
				API        string `json:"api"`
				APIKey     string `json:"apiKey"`
				AuthHeader bool   `json:"authHeader"`
				Models     []struct {
					ID string `json:"id"`
				} `json:"models"`
			} `json:"providers"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		p, ok := m.Providers[providerID]
		if len(m.Providers) != 1 || !ok || p.BaseURL != tt.wantBase || p.API != tt.wantAPI || p.AuthHeader != tt.wantAuthHeader ||
			p.APIKey != "$"+keyEnv || len(p.Models) != 1 || p.Models[0].ID != "openai/gpt-x" {
			t.Errorf("%q: providers = %+v", tt.api, m.Providers)
		}
	}
	gw := runner.Gateway{BaseURL: "http://gw"}
	for name, cfg := range map[string]runner.SessionConfig{
		"no gateway":  {Model: "m"},
		"no model":    {Gateway: gw},
		"unknown API": {Model: "m", Gateway: runner.Gateway{BaseURL: "http://gw", API: "grpc"}},
	} {
		if _, err := Models(cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestArgsLoadNothingFromTheRepoOrHome(t *testing.T) {
	args, err := Args(runner.SessionConfig{Model: "openai/gpt-x", AllowedTools: []string{"read", "bash:readonly"}}, "/tmp/a/role.md", "/tmp/a/ro.ts")
	if err != nil {
		t.Fatal(err)
	}
	joined := " " + strings.Join(args, " ") + " "
	for _, want := range []string{" --mode rpc ", " --no-session ", " --provider frankie --model openai/gpt-x ", " --no-extensions ",
		" --no-skills ", " --no-prompt-templates ", " --no-themes ", " --no-mcp ", " --no-approve ", " --offline ",
		" --tools read,bash ", " --extension /tmp/a/ro.ts ", " --append-system-prompt /tmp/a/role.md "} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q lack %q", joined, want)
		}
	}
	defaults, err := Args(runner.SessionConfig{Model: "m"}, "", "")
	if err != nil || slices.Contains(defaults, "--tools") || slices.Contains(defaults, "--extension") || slices.Contains(defaults, "--append-system-prompt") {
		t.Errorf("default args = %v, %v", defaults, err)
	}
	if _, err := Args(runner.SessionConfig{Model: "m", AllowedTools: []string{"bash:readonly"}}, "", ""); err == nil {
		t.Error("a read-only shell without its extension was accepted")
	}
}

func TestEnvironment(t *testing.T) {
	env, err := environment(runner.SessionConfig{Gateway: runner.Gateway{APIKey: "sk-v"}}, "/tmp/agent")
	if err != nil {
		t.Fatal(err)
	}
	var commands []string
	if err := json.Unmarshal([]byte(env[readonlyEnv]), &commands); err != nil || !slices.Equal(commands, runner.ReadOnlyCommands) {
		t.Errorf("%s = %q", readonlyEnv, env[readonlyEnv])
	}
	for k, want := range map[string]string{"PI_CODING_AGENT_DIR": "/tmp/agent", keyEnv: "sk-v", "PI_OFFLINE": "1", "PI_TELEMETRY": "0", "PI_SKIP_VERSION_CHECK": "1"} {
		if env[k] != want {
			t.Errorf("%s = %q, want %q", k, env[k], want)
		}
	}
}

// D54: pi loads only its built-in MCP support, reads only the session's
// servers from its agent directory, and names their tools in an allowlist.
func TestMCPConfig(t *testing.T) {
	cfg := runner.SessionConfig{Model: "m", AllowedTools: []string{"read"},
		MCPServers: []runner.MCPServer{{Name: "frankie", URL: "http://127.0.0.1:9000", Tools: []string{"ask_human"}}}}
	raw, err := MCP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		MCPServers map[string]struct {
			URL      string `json:"url"`
			Exposure string `json:"exposure"`
			Timeout  int64  `json:"timeout"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if s := m.MCPServers["frankie"]; s.URL != "http://127.0.0.1:9000" || s.Exposure != "direct" || s.Timeout != int64(runner.MCPToolTimeout.Seconds()) {
		t.Errorf("mcp.json = %s", raw)
	}
	args, err := Args(cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	joined := " " + strings.Join(args, " ") + " "
	for _, want := range []string{" --extension builtin:mcp ", " --tools read,mcp__frankie__ask_human ", " --no-extensions "} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q lack %q", joined, want)
		}
	}
	if strings.Contains(joined, "--no-mcp") {
		t.Errorf("args %q disable MCP", joined)
	}
	plain, _ := Args(runner.SessionConfig{Model: "m"}, "", "")
	if !slices.Contains(plain, "--no-mcp") || slices.Contains(plain, "builtin:mcp") {
		t.Errorf("without servers: %v", plain)
	}
}
