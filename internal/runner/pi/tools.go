package pi

import (
	_ "embed"
	"fmt"
	"slices"
)

// builtinTools maps the harness-neutral tool names of a role's allowed_tools
// (docs/spec/03) to pi's built-in tools. pi has no web fetch tool.
var builtinTools = map[string][]string{
	"read":          {"read"},
	"grep":          {"grep"},
	"glob":          {"find", "ls"},
	"edit":          {"edit"},
	"write":         {"write"},
	"bash":          {"bash"},
	"bash:readonly": {"bash"},
}

// readonlyExtension restricts pi's bash tool to read-only commands; pi has
// no permission rules of its own.
//
//go:embed readonly.ts
var readonlyExtension []byte

// tools returns the --tools allowlist for a role's tools, and whether the
// shell must be read-only. No list keeps pi's defaults (read, bash, edit,
// write): the pod is the sandbox.
func tools(allowed []string) (list []string, readonly bool, err error) {
	if slices.Contains(allowed, "bash") && slices.Contains(allowed, "bash:readonly") {
		return nil, false, fmt.Errorf("pi: bash and bash:readonly together")
	}
	for _, name := range allowed {
		ts, ok := builtinTools[name]
		if !ok {
			return nil, false, fmt.Errorf("pi: tool %q is not available in pi", name)
		}
		for _, t := range ts {
			if !slices.Contains(list, t) {
				list = append(list, t)
			}
		}
	}
	return list, slices.Contains(allowed, "bash:readonly"), nil
}
