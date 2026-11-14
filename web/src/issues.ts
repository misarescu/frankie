// Issues as the control plane API returns them (internal/api/issues.go, D59),
// starting them (D62), and the board's tree and dependency graph.

import { signed } from "./identity";
import type { LiveRunRef } from "./live";

export type Project = { id: string; name: string; key_prefix: string };

export type IssueSummary = {
  key: string;
  number: number;
  type: string;
  title: string;
  status: string;
  priority: number;
  parent?: string;
  depends_on: string[];
  labels: string[];
  source_ref?: string;
  cost_usd: number;
  latest_run?: { id: string; status: string };
  start_requested_at?: string;
};

export type IssueRef = { key: string; type: string; title: string; status: string };

export type RefinedSpec = { spec: string; acceptance_criteria: string[]; file_hints?: string[]; summary?: string };

export type Overlap = { key: string; with: string; paths: string[] };

export type IssueDetail = {
  key: string;
  number: number;
  project: string;
  type: string;
  title: string;
  status: string;
  priority: number;
  spec: string;
  acceptance_criteria: string[];
  labels: string[];
  source_ref?: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  start_requested_at?: string;
  start_requested_by?: string;
  spec_approved_at?: string;
  spec_approved_by?: string;
  spec_feedback?: string;
  spec_proposal?: RefinedSpec;
  // Who completed or canceled the issue, when, and why (D70).
  closed_at?: string;
  closed_by?: string;
  close_reason?: string;
  parent?: IssueRef;
  children: IssueRef[];
  depends_on: IssueRef[];
  dependents: IssueRef[];
  runs: { id: string; status: string; workflow: string; created_at: string; cost_usd: number }[];
  // The runs in progress on this issue and on the issues under it (M5.1).
  live_runs: LiveRunRef[];
  usage: { cost_usd: number; input_tokens: number; output_tokens: number };
  change_requests: { repo: string; number: number; url: string; status: string }[];
  file_hints: string[];
  changed_paths: string[];
  overlaps: Overlap[];
};

export type StartReply = {
  requested: string[];
  starting: string[];
  waiting: { key: string; reason: string }[];
  overlaps: Overlap[];
};

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

async function post<T>(fetchFn: Fetch, url: string, body: unknown): Promise<T> {
  const r = await fetchFn(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  if (r.status !== 202 && r.status !== 200) throw new Error((await r.text()).trim() || `status ${r.status}`);
  return r.json() as Promise<T>;
}

// startIssues asks the scheduler to start issues; a parent covers everything
// under it. The reply is the scheduler's plan for them.
export const startIssues = (fetchFn: Fetch, keys: string[], by?: string | null): Promise<StartReply> =>
  post<StartReply>(fetchFn, "/api/issues/start", signed({ keys }, by));

// retryIssue re-arms a failed issue and asks to start it.
export const retryIssue = (fetchFn: Fetch, key: string, by?: string | null): Promise<IssueRef> =>
  post<IssueRef>(fetchFn, `/api/issues/${encodeURIComponent(key)}/retry`, signed({}, by));

export type CloseReply = { closed: IssueRef[] };

// completeIssue moves a parent whose sub-issues are all finished to merged
// (D70); Frankie then closes its provider issue as completed (D71).
export const completeIssue = (fetchFn: Fetch, key: string, by?: string | null): Promise<CloseReply> =>
  post<CloseReply>(fetchFn, `/api/issues/${encodeURIComponent(key)}/complete`, signed({}, by));

// cancelIssue cancels an issue and everything unfinished under it, saying why
// (D70); Frankie then closes their provider issues as not planned (D71).
export async function cancelIssue(fetchFn: Fetch, key: string, reason: string, by?: string | null): Promise<CloseReply> {
  const r = reason.trim();
  if (r === "") throw new Error("say why it is canceled");
  return post<CloseReply>(fetchFn, `/api/issues/${encodeURIComponent(key)}/cancel`, signed({ reason: r }, by));
}

export type Progress = { merged: number; canceled: number; total: number };

// progress counts a parent's sub-issues (D70): merged (or released), canceled,
// and all. The issue page uses the direct sub-issues, which completing needs
// finished; the issue list counts the leaves under a parent, where the work is.
export function progress(statuses: string[]): Progress {
  return {
    merged: statuses.filter((s) => s === "merged" || s === "released").length,
    canceled: statuses.filter((s) => s === "canceled").length,
    total: statuses.length,
  };
}

// leafStatuses lists the statuses of the leaves under key: the issues under it
// that have no sub-issues. A leaf itself has none under it.
export function leafStatuses(issues: IssueSummary[], key: string): string[] {
  const children = new Map<string, IssueSummary[]>();
  for (const i of issues) if (i.parent) children.set(i.parent, [...(children.get(i.parent) ?? []), i]);
  const out: string[] = [];
  const seen = new Set<string>([key]);
  const walk = (k: string) => {
    for (const c of children.get(k) ?? []) {
      if (seen.has(c.key)) continue;
      seen.add(c.key);
      if (children.has(c.key)) walk(c.key);
      else out.push(c.status);
    }
  };
  walk(key);
  return out;
}

// progressText says how far a parent is: "2 of 3 merged, 1 canceled".
export function progressText(p: Progress): string {
  return `${p.merged} of ${p.total} merged${p.canceled > 0 ? `, ${p.canceled} canceled` : ""}`;
}

// canComplete mirrors domain.CheckComplete, so the page offers Complete only
// when the API would accept it: a draft or ready parent whose sub-issues are all
// finished, at least one not canceled.
export function canComplete(is: Pick<IssueDetail, "status" | "children">): boolean {
  const p = progress(is.children.map((c) => c.status));
  return (is.status === "draft" || is.status === "ready") && p.total > 0 &&
    is.children.every((c) => isFinished(c.status)) && p.canceled < p.total;
}

// shortID shortens a run's ID for lists. IDs are UUIDv7, whose leading digits
// are a timestamp shared by runs started close together, so it keeps the end.
export const shortID = (id: string): string => id.slice(-8);

// Finished issues: on main, released, or dropped.
const finished = new Set(["merged", "released", "canceled"]);
export const isFinished = (status: string): boolean => finished.has(status);

export type TreeRow = { issue: IssueSummary; depth: number };

// tree orders issues as their hierarchy, parents before children, keeping the
// API's order (priority, then number) among siblings. An issue whose parent is
// not in the list is a root. hide drops issues, and their subtrees, unless a
// descendant is kept.
export function tree(issues: IssueSummary[], hide: (i: IssueSummary) => boolean = () => false): TreeRow[] {
  const keys = new Set(issues.map((i) => i.key));
  const children = new Map<string, IssueSummary[]>();
  const roots: IssueSummary[] = [];
  for (const i of issues) {
    if (i.parent && keys.has(i.parent) && i.parent !== i.key) {
      children.set(i.parent, [...(children.get(i.parent) ?? []), i]);
    } else {
      roots.push(i);
    }
  }
  const out: TreeRow[] = [];
  const seen = new Set<string>();
  const walk = (i: IssueSummary, depth: number): boolean => {
    if (seen.has(i.key)) return false;
    seen.add(i.key);
    const at = out.length;
    out.push({ issue: i, depth });
    let keep = !hide(i);
    for (const c of children.get(i.key) ?? []) keep = walk(c, depth + 1) || keep;
    if (!keep) out.splice(at, 1); // its children were dropped too: none was kept
    return keep;
  };
  for (const r of roots) walk(r, 0);
  return out;
}

export type GraphNode = { key: string; title: string; status: string; layer: number; row: number };
export type GraphEdge = { from: string; to: string }; // from waits for to

// dependencyGraph lays out the issues that take part in a dependency, in
// layers: an issue sits one layer right of the furthest issue it waits for, so
// edges point left to what must finish first. Rows keep the API's order within
// a layer. A cycle (the store rejects them) cannot loop: its issues stay in
// layer 0.
export function dependencyGraph(issues: IssueSummary[]): { nodes: GraphNode[]; edges: GraphEdge[] } {
  const byKey = new Map(issues.map((i) => [i.key, i]));
  const edges: GraphEdge[] = [];
  const involved = new Set<string>();
  for (const i of issues) {
    for (const d of i.depends_on) {
      if (!byKey.has(d)) continue;
      edges.push({ from: i.key, to: d });
      involved.add(i.key);
      involved.add(d);
    }
  }
  const layer = new Map<string, number>();
  const visiting = new Set<string>();
  const depth = (key: string): number => {
    const known = layer.get(key);
    if (known !== undefined) return known;
    if (visiting.has(key)) return 0;
    visiting.add(key);
    let l = 0;
    for (const d of byKey.get(key)?.depends_on ?? []) {
      if (byKey.has(d)) l = Math.max(l, depth(d) + 1);
    }
    visiting.delete(key);
    layer.set(key, l);
    return l;
  };
  const rows = new Map<number, number>();
  const nodes: GraphNode[] = [];
  for (const i of issues) {
    if (!involved.has(i.key)) continue;
    const l = depth(i.key);
    const row = rows.get(l) ?? 0;
    rows.set(l, row + 1);
    nodes.push({ key: i.key, title: i.title, status: i.status, layer: l, row });
  }
  return { nodes, edges };
}
