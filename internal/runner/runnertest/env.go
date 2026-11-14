package runnertest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Binary finds a harness binary on PATH (mise puts the pinned versions
// there). Without it the test skips locally and fails in CI.
func Binary(t *testing.T, name string) string {
	t.Helper()
	bin, err := exec.LookPath(name)
	if err == nil {
		return bin
	}
	if os.Getenv("CI") != "" {
		t.Fatalf("%s not on PATH (required in CI): %v", name, err)
	}
	t.Skipf("%s not on PATH; run through mise (mise run test): %v", name, err)
	return ""
}

// DeadProxy refuses every connection: traffic that ignores NO_PROXY fails.
const DeadProxy = "http://127.0.0.1:9"

// PodNoProxy is the agent pod's NO_PROXY (internal/sandbox).
const PodNoProxy = "localhost,127.0.0.1,.svc,.cluster.local"

// PodEnv is an environment like an agent pod's: a fresh home, isolated from
// the developer's harness data and configuration, and everything through the
// egress proxy (here a dead one) except cluster-internal names and loopback.
func PodEnv(t *testing.T) map[string]string {
	home := t.TempDir()
	return map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"), "XDG_DATA_HOME": filepath.Join(home, ".local/share"),
		"XDG_STATE_HOME": filepath.Join(home, ".local/state"), "XDG_CACHE_HOME": filepath.Join(home, ".cache"),
		"HTTPS_PROXY": DeadProxy, "https_proxy": DeadProxy, "HTTP_PROXY": DeadProxy, "http_proxy": DeadProxy,
		"NO_PROXY": PodNoProxy, "no_proxy": PodNoProxy,
	}
}
