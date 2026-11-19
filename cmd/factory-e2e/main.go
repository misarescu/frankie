// Command factory-e2e is the factory's own end-to-end check (D132), which
// Frankie's preview stage runs as its e2e suite (mise run e2e:factory) in a
// verification pod against a preview of the factory: it waits for the
// preview's issue to end and writes each step as a JUnit test
// (internal/factorye2e). It exits 1 when the check failed.
//
// Environment: E2E_BASE_URL (the preview's API, required), E2E_RESULTS (the
// JUnit file, default e2e-results.xml), FACTORY_E2E_PROJECT (default
// misarescu/frankie-dummy) and FACTORY_E2E_TIMEOUT (default 25m, under the
// verification pod's 30-minute deadline).
package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/misarescu/frankie/internal/factorye2e"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Getenv, os.Stdout, 15*time.Second)
	stop()
	os.Exit(code)
}

// run checks the preview and returns the exit code: 0 passed, 1 failed, 2 not
// run (bad environment, results not written).
func run(ctx context.Context, env func(string) string, out io.Writer, poll time.Duration) int {
	say := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format+"\n", args...) }
	base := env("E2E_BASE_URL")
	if base == "" {
		say("factory-e2e: E2E_BASE_URL is not set")
		return 2
	}
	timeout := 25 * time.Minute
	if v := env("FACTORY_E2E_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			say("factory-e2e: FACTORY_E2E_TIMEOUT %q is not a duration", v)
			return 2
		}
		timeout = d
	}
	cfg := factorye2e.Config{BaseURL: base, Project: cmp.Or(env("FACTORY_E2E_PROJECT"), "misarescu/frankie-dummy"),
		Timeout: timeout, Poll: poll, HTTP: &http.Client{Timeout: 30 * time.Second}, Log: out}
	say("factory-e2e: waiting up to %s for %s's issue in the preview at %s", timeout, cfg.Project, base)
	r := factorye2e.Run(ctx, cfg)

	b, err := r.JUnit()
	if err == nil {
		err = os.WriteFile(cmp.Or(env("E2E_RESULTS"), "e2e-results.xml"), b, 0o644)
	}
	if err != nil {
		say("factory-e2e: write the results: %v", err)
		return 2
	}
	for _, c := range r.Cases {
		switch {
		case c.Failure != "":
			say("FAIL  %s > %s: %s", c.Suite, c.Name, c.Failure)
		case c.Error != "":
			say("ERROR %s > %s: %s", c.Suite, c.Name, c.Error)
		default:
			say("ok    %s > %s", c.Suite, c.Name)
		}
	}
	if !r.Passed() {
		return 1
	}
	return 0
}
