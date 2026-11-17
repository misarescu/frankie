package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runScript(t *testing.T, command string) (string, int) {
	t.Helper()
	results := filepath.Join(t.TempDir(), "results.xml")
	cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", Script(command, "E2E_RESULTS"))
	cmd.Env = append(os.Environ(), "E2E_RESULTS="+results)
	out, err := cmd.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// M7, D116: the script keeps the suite's exit code and prints its results.
func TestScript(t *testing.T) {
	log, code := runScript(t, `echo running; echo '<testsuite name="s"/>' > "$E2E_RESULTS"; exit 3`)
	if code != 3 {
		t.Errorf("exit = %d, want the suite's 3", code)
	}
	results, found := Results([]byte(log))
	if !found || string(results) != `<testsuite name="s"/>` {
		t.Errorf("results = %q, %v from log:\n%s", results, found, log)
	}
	if before := Before([]byte(log)); strings.TrimSpace(before) != "running" {
		t.Errorf("before = %q", before)
	}

	log, code = runScript(t, "exit 0")
	if _, found := Results([]byte(log)); found || code != 0 {
		t.Errorf("no results file: found %v, exit %d", found, code)
	}
	// A suite that exits from inside its command still prints its results.
	log, code = runScript(t, `echo '<testsuite/>' > "$E2E_RESULTS"; exit 1`)
	if _, found := Results([]byte(log)); !found || code != 1 {
		t.Errorf("exit inside the command: found %v, exit %d", found, code)
	}
}

func TestResultsTakesTheLastCopy(t *testing.T) {
	log := "<<<frankie-e2e-results>>>\nold\n<<<frankie-e2e-results-end>>>\n<<<frankie-e2e-results>>>\nnew\n<<<frankie-e2e-results-end>>>\n"
	if r, ok := Results([]byte(log)); !ok || string(r) != "new" {
		t.Errorf("results = %q, %v", r, ok)
	}
	if _, ok := Results([]byte("<<<frankie-e2e-results>>>\ncut off")); ok {
		t.Error("results without an end marker accepted")
	}
}
