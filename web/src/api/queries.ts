// Server state (D141): one queryOptions factory per resource of the control
// plane API, under the query keys every page shares, so a page, the sidebar
// and the palette reading one resource make one request and see one answer.

import { queryOptions } from "@tanstack/react-query";
import type { Approval } from "@/approvals";
import type { RunEvent, RunSummary } from "@/events";
import { detailWithTotal, summaryWithTotal, type IssueDetail, type IssueSummary, type Project } from "@/issues";
import type { Question } from "@/questions";
import { isLive, type ReleaseDetail, type ReleaseSummary } from "@/releases";
import type { Spec } from "@/specs";
import type { PlanStep, Step } from "@/steps";

export const keys = {
  projects: ["projects"] as const,
  issues: (project: string) => ["issues", project] as const,
  issue: (key: string) => ["issue", key] as const,
  runs: ["runs"] as const,
  runSteps: (id: string) => ["run", id, "steps"] as const,
  runPlan: (id: string) => ["run", id, "plan"] as const,
  runEvents: (id: string) => ["run", id, "events"] as const,
  approvals: ["approvals"] as const,
  questions: ["questions"] as const,
  specs: ["specs"] as const,
  releases: (project: string) => ["releases", project] as const,
  release: (id: string) => ["release", id] as const,
};

// ApiError is a read the API refused, with what it said.
export class ApiError extends Error {
  constructor(readonly status: number, message: string) {
    super(message);
  }
}

// getJSON reads one API resource. A failed request says why in words a
// person can act on: the control plane down (the web server answers 502), the
// web server gone, or what the API answered.
export async function getJSON<T>(url: string, fetchFn: typeof fetch = fetch): Promise<T> {
  let r: Response;
  try {
    r = await fetchFn(url);
  } catch {
    throw new ApiError(0, "the web server did not answer; check your connection");
  }
  if (r.status === 502) throw new ApiError(502, "the control plane is not reachable");
  if (!r.ok) throw new ApiError(r.status, (await r.text()).trim() || `status ${r.status}`);
  return r.json() as Promise<T>;
}

const enc = encodeURIComponent;

export const projectsQuery = () => queryOptions({
  queryKey: keys.projects,
  queryFn: () => getJSON<Project[]>("/api/projects"),
  staleTime: 5 * 60_000,
});

export const issuesQuery = (project: string) => queryOptions({
  queryKey: keys.issues(project),
  queryFn: () => getJSON<IssueSummary[]>(`/api/issues?project=${enc(project)}`).then((l) => l.map(summaryWithTotal)),
  refetchInterval: 10_000,
});

export const issueQuery = (key: string) => queryOptions({
  queryKey: keys.issue(key),
  queryFn: () => getJSON<IssueDetail>(`/api/issues/${enc(key)}`).then(detailWithTotal),
  refetchInterval: (q) => ((q.state.data?.live_runs.length ?? 0) > 0 ? 3_000 : 5_000),
});

export const runsQuery = () => queryOptions({
  queryKey: keys.runs,
  queryFn: () => getJSON<RunSummary[]>("/api/runs"),
  refetchInterval: 5_000,
});

// runStepsQuery polls a run's steps while it goes (live), and stops once it
// ended; the stream's state changes refresh it in between.
export const runStepsQuery = (id: string, live: boolean) => queryOptions({
  queryKey: keys.runSteps(id),
  queryFn: () => getJSON<Step[]>(`/api/runs/${enc(id)}/steps`),
  refetchInterval: live ? 3_000 : false,
});

// A run's plan is its snapshotted workflow (invariant 4): it never changes.
export const runPlanQuery = (id: string) => queryOptions({
  queryKey: keys.runPlan(id),
  queryFn: () => getJSON<PlanStep[]>(`/api/runs/${enc(id)}/plan`),
  staleTime: Infinity,
});

// RunEvents is what the stream has delivered of a run's transcript (stream.ts):
// its events in seq order, how it ended once it did, and whether the
// connection dropped and the browser is reconnecting.
export type RunEvents = { events: RunEvent[]; end: string | null; reconnecting: boolean };
export const noEvents: RunEvents = { events: [], end: null, reconnecting: false };

export const approvalsQuery = () => queryOptions({
  queryKey: keys.approvals,
  queryFn: () => getJSON<Approval[]>("/api/approvals"),
  refetchInterval: 5_000,
});

export const questionsQuery = () => queryOptions({
  queryKey: keys.questions,
  queryFn: () => getJSON<Question[]>("/api/questions"),
  refetchInterval: 5_000,
});

export const specsQuery = () => queryOptions({
  queryKey: keys.specs,
  queryFn: () => getJSON<Spec[]>("/api/specs"),
  refetchInterval: 5_000,
});

export const releasesQuery = (project: string) => queryOptions({
  queryKey: keys.releases(project),
  queryFn: () => getJSON<ReleaseSummary[]>(`/api/releases?project=${enc(project)}`),
  refetchInterval: 10_000,
});

export const releaseQuery = (id: string) => queryOptions({
  queryKey: keys.release(id),
  queryFn: () => getJSON<ReleaseDetail>(`/api/releases/${enc(id)}`),
  refetchInterval: (q) => (q.state.data && isLive(q.state.data.status) ? 3_000 : 15_000),
});
