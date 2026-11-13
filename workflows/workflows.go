// Package workflows holds the platform's built-in workflow definitions
// (docs/spec/03-workflows-and-gates.md). A project uses one by name, or adds its
// own in `.factory/workflows/` of its repo.
package workflows

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

//go:embed *.yaml
var files embed.FS

// Default is the definition a project runs when it names none.
const Default = "default-build"

// ErrNotFound reports a name with no built-in definition.
var ErrNotFound = errors.New("no built-in workflow with that name")

// Names lists the built-in definitions, sorted.
func Names() []string {
	entries, _ := fs.ReadDir(files, ".")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), path.Ext(e.Name())))
	}
	slices.Sort(names)
	return names
}

// Get parses and validates the built-in definition called name.
func Get(name string) (config.Workflow, error) {
	raw, err := files.ReadFile(name + ".yaml")
	if err != nil {
		return config.Workflow{}, fmt.Errorf("workflows: %q: %w", name, ErrNotFound)
	}
	w, err := config.ParseWorkflow(raw)
	if err != nil {
		return config.Workflow{}, fmt.Errorf("workflows: built-in %q: %w", name, err)
	}
	if w.Name != name {
		return config.Workflow{}, fmt.Errorf("workflows: built-in file %s.yaml is named %q", name, w.Name)
	}
	return w, nil
}
