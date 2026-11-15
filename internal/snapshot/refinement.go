package snapshot

import (
	"context"
	"fmt"

	"github.com/misarescu/frankie/internal/config"
)

// RefinementName is the name of the definition refinement runs, as recorded on
// its Run ("refine@1").
const RefinementName = "refine"

// refinementDefinition is what refinement runs (D65): one read-only agent step
// with the refiner role. It is not a workflow projects can select.
const refinementDefinition = `
name: ` + RefinementName + `
version: 1
steps:
  - id: refine
    agent: {role: refiner}
`

// ResolveRefinement snapshots what an issue's refinement executes, like Resolve
// does for its runs: the refinement definition, the refiner role (the repo's
// `.factory/roles/refiner.yaml` when it has one, else the built-in) and the
// project's issue budget, all read at the head of baseBranch.
func ResolveRefinement(ctx context.Context, r FileReader, repo, baseBranch string) (Snapshot, error) {
	rd, err := open(ctx, r, repo, baseBranch)
	if err != nil {
		return Snapshot{}, err
	}
	project, err := rd.project()
	if err != nil {
		return Snapshot{}, err
	}
	w, err := config.ParseWorkflow([]byte(refinementDefinition))
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot: refinement definition: %w", err) // a platform bug, not the repo's
	}
	s := Snapshot{Workflow: w, Source: "built-in", BaseSHA: rd.sha, IssueBudgetUSD: project.Budgets.IssueUSD,
		EgressAllow: project.Egress.Allow}
	if s.Roles, err = rolesResult(config.ResolveRefinementRoles(w, rd.roleLookup())); err != nil {
		return Snapshot{}, err
	}
	return s, nil
}
