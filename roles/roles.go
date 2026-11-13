// Package roles holds the platform's built-in roles and the output contracts
// they may declare (docs/spec/03-workflows-and-gates.md, Roles). A project's
// repo may add or replace roles in `.factory/roles/`; those are parsed with
// Parse too, so every role returning a contract gets the same instructions.
package roles

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/misarescu/frankie/internal/config"
)

//go:embed *.yaml prompts/*.md schemas/*.json
var files embed.FS

// ErrNotFound reports a name with no built-in role.
var ErrNotFound = errors.New("no built-in role with that name")

// contractSchemas maps each output contract to its JSON Schema file.
var contractSchemas = map[string]string{
	config.ContractReviewVerdict: "schemas/review-verdict.json",
}

// Names lists the built-in roles, sorted.
func Names() []string {
	entries, _ := fs.ReadDir(files, ".")
	var names []string
	for _, e := range entries {
		if !e.IsDir() && path.Ext(e.Name()) == ".yaml" {
			names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
		}
	}
	slices.Sort(names)
	return names
}

// Get parses the built-in role called name.
func Get(name string) (config.Role, error) {
	raw, err := files.ReadFile(name + ".yaml")
	if err != nil {
		return config.Role{}, fmt.Errorf("roles: %q: %w", name, ErrNotFound)
	}
	r, err := Parse(raw, func(p string) ([]byte, error) { return files.ReadFile(p) })
	if err != nil {
		return config.Role{}, fmt.Errorf("roles: built-in %q: %w", name, err)
	}
	if r.Name != name {
		return config.Role{}, fmt.Errorf("roles: built-in file %s.yaml is named %q", name, r.Name)
	}
	return r, nil
}

// Parse parses a role file (config.ParseRole) and, when the role declares an
// output contract, appends the platform's instructions for it to the system
// prompt: the agent learns the exact format from the platform, not from a
// prompt that may drift from what the gate accepts.
func Parse(raw []byte, readFile func(name string) ([]byte, error)) (config.Role, error) {
	r, err := config.ParseRole(raw, readFile)
	if err != nil || r.OutputContract == "" {
		return r, err
	}
	schema, err := ContractSchema(r.OutputContract)
	if err != nil {
		return config.Role{}, err
	}
	r.SystemPrompt = strings.TrimRight(r.SystemPrompt, "\n") + "\n\n" +
		"Your final message must be only your result: one JSON object, with no other text and no code fence, matching this JSON Schema:\n\n" +
		string(schema)
	return r, nil
}

// ContractSchema returns the JSON Schema of an output contract.
func ContractSchema(contract string) ([]byte, error) {
	p, ok := contractSchemas[contract]
	if !ok {
		return nil, fmt.Errorf("roles: unknown output contract %q", contract)
	}
	return files.ReadFile(p)
}
