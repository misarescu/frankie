// The runs page (M9): a project's runs from the global run list, newest
// first, and its filters.

import type { RunSummary } from "./events";
import { keyPrefix } from "./issues";
import type { RunsFilter } from "./paths";

// projectRuns keeps the runs of a project: those of its issues (by key
// prefix) and the release agent's runs of its releases, newest first.
export function projectRuns(runs: RunSummary[], keyPrefixOf: string, releaseIDs: ReadonlySet<string>): RunSummary[] {
  return runs
    .filter((r) => (r.ReleaseID ? releaseIDs.has(r.ReleaseID) : keyPrefix(r.IssueKey) === keyPrefixOf))
    .sort((a, b) => Date.parse(b.CreatedAt) - Date.parse(a.CreatedAt));
}

// A run is active while it is pending or running; it ends succeeded, failed
// or canceled.
export const isActiveRun = (status: string): boolean => status === "pending" || status === "running";

export const filterRuns = (runs: RunSummary[], f: RunsFilter | undefined): RunSummary[] =>
  f === "active" ? runs.filter((r) => isActiveRun(r.Status)) : f === "failed" ? runs.filter((r) => r.Status === "failed") : runs;
