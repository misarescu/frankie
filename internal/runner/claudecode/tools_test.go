package claudecode

import (
	"slices"
	"strings"
	"testing"
)

func TestToolArgs(t *testing.T) {
	t.Run("no list: every tool, no prompts", func(t *testing.T) {
		got, err := toolArgs(nil, nil)
		if err != nil || !slices.Equal(got, []string{"--dangerously-skip-permissions"}) {
			t.Fatalf("args = %v, %v", got, err)
		}
	})
	t.Run("a list without a restricted shell limits the built-in tools", func(t *testing.T) {
		got, err := toolArgs([]string{"read", "edit", "bash", "read"}, nil)
		if err != nil || !slices.Equal(got, []string{"--tools", "Read,Edit,Bash", "--dangerously-skip-permissions"}) {
			t.Fatalf("args = %v, %v", got, err)
		}
	})
	t.Run("bash:readonly allows only read-only commands, without prompts", func(t *testing.T) {
		got, err := toolArgs([]string{"read", "grep", "glob", "bash:readonly"}, []string{"mcp__frankie__ask_human"})
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(got, " ")
		if slices.Contains(got, "--dangerously-skip-permissions") {
			t.Fatalf("a restricted shell must not bypass permissions: %v", got)
		}
		for _, want := range []string{"--tools Read,Grep,Glob,Bash", "--permission-mode dontAsk", "Bash(git diff *)", "Bash(ls)", "Read,Grep,Glob,", "mcp__frankie__ask_human"} {
			if !strings.Contains(joined, want) {
				t.Errorf("args %q missing %q", joined, want)
			}
		}
		for _, unwanted := range []string{"Bash(git push", "Bash(git commit", "Bash(rm", "Write", "Edit"} {
			if strings.Contains(joined, unwanted) {
				t.Errorf("args %q allow %q", joined, unwanted)
			}
		}
	})
	t.Run("refusals", func(t *testing.T) {
		for _, list := range [][]string{{"kubectl"}, {"bash", "bash:readonly"}} {
			if _, err := toolArgs(list, nil); err == nil {
				t.Errorf("%v accepted", list)
			}
		}
	})
}
