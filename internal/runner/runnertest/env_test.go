package runnertest

import "testing"

func TestPodEnv(t *testing.T) {
	env := PodEnv(t)
	if env["HTTPS_PROXY"] != DeadProxy || env["NO_PROXY"] != PodNoProxy || env["HOME"] == "" {
		t.Errorf("env = %v", env)
	}
	if home := HomeEnv(t); home["HOME"] == "" || home["HTTPS_PROXY"] != "" {
		t.Errorf("home env = %v", home)
	}
}
