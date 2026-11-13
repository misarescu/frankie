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
