package controlplane

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	err := os.WriteFile(path, []byte(`{
	  "repos": [{"name": "misarescu/Goodreads-Clone", "upstream": "https://github.com/misarescu/Goodreads-Clone.git", "keyPrefix": "gc"}],
	  "intakeLabel": "factory", "pollInterval": "30s",
	  "temporal": {"address": "frankie-temporal:7233", "namespace": "frankie"},
	  "gatewayAdminURL": "http://frankie-llm-gateway:4000",
	  "agent": {"namespace": "frankie-agents", "runnerImage": "img", "cpu": "2", "memory": "3Gi", "workspaceSize": "10Gi",
	            "podDeadline": "2h", "sessionTimeout": "2h", "idleTimeout": "20m", "model": "claude-sonnet-5-5", "budgetUSD": 2}
	}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := LoadConfigFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].BaseBranch != "main" || cfg.Repos[0].KeyPrefix != "gc" {
		t.Errorf("repos = %+v", cfg.Repos)
	}
	a := cfg.Agent
	// Two durations with the same value must both be set.
	if a.PodDeadline != 2*time.Hour || a.SessionTimeout != 2*time.Hour || a.IdleTimeout != 20*time.Minute || cfg.PollInterval != 30*time.Second {
		t.Errorf("durations: deadline %s session %s idle %s poll %s", a.PodDeadline, a.SessionTimeout, a.IdleTimeout, cfg.PollInterval)
	}
	if a.Limits.Memory().String() != "3Gi" || a.WorkspaceSize.String() != "10Gi" || a.BudgetUSD != 2 {
		t.Errorf("agent = %+v", a)
	}
	if cfg.TemporalAddress != "frankie-temporal:7233" || cfg.TemporalNamespace != "frankie" || cfg.IntakeLabel != "factory" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadConfigFileErrors(t *testing.T) {
	for name, body := range map[string]string{
		"not json":     `{`,
		"bad duration": `{"pollInterval": "soon"}`,
		"bad quantity": `{"agent": {"cpu": "lots", "memory": "1Gi"}}`,
	} {
		path := filepath.Join(t.TempDir(), "c.json")
		_ = os.WriteFile(path, []byte(body), 0o600)
		var cfg Config
		if err := LoadConfigFile(path, &cfg); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var cfg Config
	if err := LoadConfigFile("/does/not/exist.json", &cfg); err == nil {
		t.Error("missing file accepted")
	}
}

func TestLoadConfigFileToolchains(t *testing.T) {
	load := func(t *testing.T, body string) (Config, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		var cfg Config
		return cfg, LoadConfigFile(path, &cfg)
	}

	t.Run("configured", func(t *testing.T) {
		cfg, err := load(t, `{"toolchains": {
		  "registryURL": "http://frankie-registry.frankie.svc:5000", "pushHost": "frankie-registry.frankie.svc:5000",
		  "pullHost": "localhost:30500", "namespace": "frankie-build", "buildkitImage": "moby/buildkit:v0.35.0",
		  "runtimeClass": "kata", "serviceAccount": "frankie-build", "egressProxyURL": "http://proxy:3128",
		  "cpu": "2", "memory": "3Gi", "scratchSize": "10Gi", "deadline": "30m"}}`)
		if err != nil {
			t.Fatal(err)
		}
		tc := cfg.Toolchain
		if !tc.Enabled() || tc.PullHost != "localhost:30500" || tc.Build.PushHost != "frankie-registry.frankie.svc:5000" ||
			tc.Build.Deadline != 30*time.Minute || tc.Build.ScratchSize.String() != "10Gi" || tc.Build.Limits.Memory().String() != "3Gi" {
			t.Errorf("toolchain = %+v", tc)
		}
	})
	t.Run("absent disables toolchain images", func(t *testing.T) {
		cfg, err := load(t, `{}`)
		if err != nil || cfg.Toolchain.Enabled() {
			t.Errorf("enabled = %v, err = %v", cfg.Toolchain.Enabled(), err)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		for name, body := range map[string]string{
			"bad deadline": `{"toolchains": {"registryURL": "http://r", "pullHost": "h", "cpu": "1", "memory": "1Gi", "scratchSize": "1Gi", "deadline": "soon"}}`,
			"bad quantity": `{"toolchains": {"registryURL": "http://r", "pullHost": "h", "cpu": "lots", "memory": "1Gi", "scratchSize": "1Gi", "deadline": "1m"}}`,
			"no pull host": `{"toolchains": {"registryURL": "http://r", "cpu": "1", "memory": "1Gi", "scratchSize": "1Gi", "deadline": "1m"}}`,
		} {
			if _, err := load(t, body); err == nil {
				t.Errorf("%s: accepted", name)
			}
		}
	})
}

func TestLoadConfigFileGate(t *testing.T) {
	load := func(t *testing.T, body string) (Config, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		var cfg Config
		return cfg, LoadConfigFile(path, &cfg)
	}
	t.Run("configured", func(t *testing.T) {
		cfg, err := load(t, `{"gate": {"commands": ["make check"], "cpu": "1", "memory": "2Gi", "workspaceSize": "5Gi", "deadline": "15m"}}`)
		if err != nil {
			t.Fatal(err)
		}
		g := cfg.Gate
		if len(g.Commands) != 1 || g.Commands[0] != "make check" || g.Deadline != 15*time.Minute ||
			g.Limits.Memory().String() != "2Gi" || g.WorkspaceSize.String() != "5Gi" {
			t.Errorf("gate = %+v", g)
		}
	})
	t.Run("defaults to the agent's resources and a 30m deadline", func(t *testing.T) {
		cfg, err := load(t, `{"agent": {"cpu": "2", "memory": "3Gi", "workspaceSize": "10Gi"}}`)
		if err != nil {
			t.Fatal(err)
		}
		g := cfg.Gate
		if g.Deadline != 30*time.Minute || g.Limits.Memory().String() != "3Gi" || g.WorkspaceSize.String() != "10Gi" || g.Commands != nil {
			t.Errorf("gate = %+v", g)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		for name, body := range map[string]string{
			"blank command": `{"gate": {"commands": [" "]}}`,
			"bad deadline":  `{"gate": {"deadline": "-1m"}}`,
			"bad memory":    `{"gate": {"cpu": "1", "memory": "lots"}}`,
		} {
			if _, err := load(t, body); err == nil {
				t.Errorf("%s: accepted", name)
			}
		}
	})
}

func TestLoadConfigFileServices(t *testing.T) {
	load := func(t *testing.T, body string) (Config, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		var cfg Config
		return cfg, LoadConfigFile(path, &cfg)
	}
	cfg, err := load(t, `{"services": {"maxPerPod": 2, "cpu": "500m", "memory": "512Mi"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services.MaxPerPod != 2 || cfg.Services.Limits.Memory().String() != "512Mi" {
		t.Errorf("services = %+v", cfg.Services)
	}
	if cfg, err := load(t, `{}`); err != nil || cfg.Services.MaxPerPod != 0 {
		t.Errorf("absent: %+v, %v", cfg.Services, err)
	}
	for name, body := range map[string]string{
		"negative":     `{"services": {"maxPerPod": -1}}`,
		"bad quantity": `{"services": {"maxPerPod": 1, "cpu": "x", "memory": "512Mi"}}`,
	} {
		if _, err := load(t, body); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadConfigFileSandboxEnv(t *testing.T) {
	load := func(t *testing.T, body string) (Config, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "c.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		var cfg Config
		return cfg, LoadConfigFile(path, &cfg)
	}
	cfg, err := load(t, `{"sandboxEnv": {"GOPROXY": "http://frankie-athens.frankie.svc:3000"},
	  "toolchains": {"registryURL": "http://r", "pullHost": "h", "cpu": "1", "memory": "1Gi", "scratchSize": "1Gi", "deadline": "1m"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Agent.Env["GOPROXY"] != "http://frankie-athens.frankie.svc:3000" || cfg.Toolchain.Build.Env["GOPROXY"] == "" {
		t.Errorf("agent env %v, build env %v", cfg.Agent.Env, cfg.Toolchain.Build.Env)
	}
	for name, body := range map[string]string{
		"proxy override": `{"sandboxEnv": {"https_proxy": "http://evil"}}`,
		"bad name":       `{"sandboxEnv": {"A-B": "x"}}`,
	} {
		if _, err := load(t, body); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
