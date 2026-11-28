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

// ago says how long ago a time was, as people read it in a list.
export function ago(iso: string, now: Date = new Date()): string {
  const s = Math.round((now.getTime() - Date.parse(iso)) / 1000);
  if (!Number.isFinite(s)) return "";
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h} h ago`;
  return `${Math.floor(h / 24)} days ago`;
}
