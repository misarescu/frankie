package claudecode

import (
	"fmt"
	"slices"
	"strings"

	"github.com/misarescu/frankie/internal/runner"
)

// builtinTools maps the harness-neutral tool names of a role's allowed_tools
// (docs/spec/03) to Claude Code's built-in tools.
var builtinTools = map[string]string{
	"read":          "Read",
	"grep":          "Grep",
	"glob":          "Glob",
	"edit":          "Edit",
	"write":         "Write",
	"bash":          "Bash",
	"bash:readonly": "Bash",
	"web-fetch":     "WebFetch",
}

// toolArgs returns the flags that limit a session to the allowed tools. With
// no list the agent gets every tool and no permission prompts (the pod is the
// sandbox). With a list, --tools removes the other built-in tools; a
// bash:readonly shell additionally runs in dontAsk mode, where only the listed
// tools and read-only command patterns are allowed and everything else is
// denied without a prompt.
func toolArgs(allowed []string) ([]string, error) {
	if len(allowed) == 0 {
		return []string{"--dangerously-skip-permissions"}, nil
	}
	var tools []string
	for _, name := range allowed {
		t, ok := builtinTools[name]
		if !ok {
			return nil, fmt.Errorf("claudecode: unknown tool %q", name)
		}
		if !slices.Contains(tools, t) {
			tools = append(tools, t)
		}
	}
	args := []string{"--tools", strings.Join(tools, ",")}
	if !slices.Contains(allowed, "bash:readonly") {
		return append(args, "--dangerously-skip-permissions"), nil
	}
	if slices.Contains(allowed, "bash") {
		return nil, fmt.Errorf("claudecode: bash and bash:readonly together")
	}
	rules := slices.DeleteFunc(slices.Clone(tools), func(t string) bool { return t == "Bash" })
	for _, c := range runner.ReadOnlyCommands {
		rules = append(rules, "Bash("+c+")", "Bash("+c+" *)")
	}
	return append(args, "--permission-mode", "dontAsk", "--allowedTools", strings.Join(rules, ",")), nil
}
