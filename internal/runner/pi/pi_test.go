package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/misarescu/frankie/internal/domain"
	"github.com/misarescu/frankie/internal/runner"
)

// The behaviors every adapter shares, run against the real pi binary, are in
// conformance_test.go; these are pi's own.

func TestStartFails(t *testing.T) {
	gw := runner.Gateway{BaseURL: "http://127.0.0.1:1", APIKey: "k"}
	t.Run("without a gateway", func(t *testing.T) {
		_, err := (&Harness{Binary: "/does/not/matter", TempDir: t.TempDir()}).Start(t.Context(), runner.SessionConfig{Workdir: t.TempDir(), Model: "m"}, func(domain.EventType, any) {})
		if err == nil || !strings.Contains(err.Error(), "gateway") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("with a tool pi lacks", func(t *testing.T) {
		cfg := runner.SessionConfig{Workdir: t.TempDir(), Model: "m", Gateway: gw, AllowedTools: []string{"web-fetch"}}
		_, err := (&Harness{Binary: "/does/not/matter", TempDir: t.TempDir()}).Start(t.Context(), cfg, func(domain.EventType, any) {})
		if err == nil || !strings.Contains(err.Error(), "web-fetch") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("when pi exits at once", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "pi")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\necho broken >&2\nexit 3\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		h := &Harness{Binary: bin, TempDir: t.TempDir()}
		_, err := h.Start(t.Context(), runner.SessionConfig{Workdir: t.TempDir(), Model: "m", Gateway: gw}, func(domain.EventType, any) {})
		if err == nil || !strings.Contains(err.Error(), "pi exited") || !strings.Contains(err.Error(), "broken") {
			t.Fatalf("err = %v", err)
		}
		if entries, _ := os.ReadDir(h.TempDir); len(entries) != 0 {
			t.Errorf("temp files left behind: %v", entries)
		}
	})
}
