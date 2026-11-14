package opencode

import (
	"fmt"
	"slices"

	"github.com/misarescu/frankie/internal/runner"
)

// rule is an opencode permission rule. The last matching rule wins, and a
// resource pattern "x *" also matches the bare "x".
type rule struct {
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Effect   string `json:"effect"`
}

// toolActions maps the harness-neutral tool names of a role's allowed_tools
// (docs/spec/03) to opencode permission actions. write is part of edit.
var toolActions = map[string]string{
	"read":      "read",
	"grep":      "grep",
	"glob":      "glob",
	"edit":      "edit",
	"write":     "edit",
	"bash":      "shell",
	"web-fetch": "webfetch",
}

// permissions returns the rules for the session's agent. They are the agent's
// own (agents.build.permissions), which opencode applies after every other
// rule, including its defaults, so they decide. Every list starts with a
// catch-all, so no operation is ever left to "ask": a headless session has
// nobody to answer.
//
// With no list the agent gets every tool (the pod is the sandbox) except
// question, which would wait for a person, and websearch, which goes to
// services outside the egress allowlist. With a list everything else is
// denied, which also removes the denied tools from what the model is offered;
// bash:readonly allows only read-only command patterns.
func permissions(allowed []string) ([]rule, error) {
	if len(allowed) == 0 {
		return []rule{
			{"*", "*", "allow"},
			{"question", "*", "deny"},
			{"websearch", "*", "deny"},
		}, nil
	}
	if slices.Contains(allowed, "bash") && slices.Contains(allowed, "bash:readonly") {
		return nil, fmt.Errorf("opencode: bash and bash:readonly together")
	}
	rules := []rule{{"*", "*", "deny"}}
	for _, name := range allowed {
		if name == "bash:readonly" {
			for _, c := range runner.ReadOnlyCommands {
				rules = append(rules, rule{"shell", c + " *", "allow"})
			}
			continue
		}
		action, ok := toolActions[name]
		if !ok {
			return nil, fmt.Errorf("opencode: unknown tool %q", name)
		}
		r := rule{action, "*", "allow"}
		if !slices.Contains(rules, r) {
			rules = append(rules, r)
		}
	}
	return rules, nil
}
