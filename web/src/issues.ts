// Issues as the control plane API returns them (internal/api/issues.go, D59),
// starting them (D62), and the board's tree and dependency graph.

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
  // The issue's own model cost, and with every issue under it (D142).
  cost_usd: number;
  total_cost_usd: number;
  latest_run?: { id: string; status: string };
  // The issue's newest change request (M5.1).
  change_request?: { number: number; url: string; status: string };
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
  // The ancestor whose start request covers this issue, when it has none of
  // its own (start_requested_by is then that request's).
  start_requested_via?: string;
  spec_approved_at?: string;
  spec_approved_by?: string;
  spec_feedback?: string;
  // Set while a draft is held: its spec was sent back or its refinement
  // failed, and it waits until someone asks again (D64, D66).
  spec_rejected_at?: string;
  spec_proposal?: RefinedSpec;
  // Who completed or canceled the issue, when, and why (D70).
  closed_at?: string;
  closed_by?: string;
  close_reason?: string;
  parent?: IssueRef;
  children: IssueRef[];
  depends_on: IssueRef[];
  dependents: IssueRef[];
  runs: { id: string; status: string; workflow: string; created_at: string; started_at?: string; finished_at?: string; cost_usd: number }[];
  // The runs in progress on this issue and on the issues under it (M5.1).
  live_runs: LiveRunRef[];
  usage: { cost_usd: number; input_tokens: number; output_tokens: number };
  // The issue's cost with every issue under it (D142); its own is usage.cost_usd.
  total_cost_usd: number;
  change_requests: { repo: string; number: number; url: string; status: string }[];
  file_hints: string[];
  changed_paths: string[];
  overlaps: Overlap[];
};

// A control plane older than D142 sends no total_cost_usd; the issue's own
// cost stands in for it, so a web pod rolled out ahead of its control plane
// still shows every cost instead of failing.
export const summaryWithTotal = (i: IssueSummary): IssueSummary =>
  ({ ...i, total_cost_usd: (i as Partial<IssueSummary>).total_cost_usd ?? i.cost_usd });
export const detailWithTotal = (i: IssueDetail): IssueDetail =>
  ({ ...i, total_cost_usd: (i as Partial<IssueDetail>).total_cost_usd ?? i.usage.cost_usd });

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
export const startIssues = (fetchFn: Fetch, keys: string[]): Promise<StartReply> =>
  post<StartReply>(fetchFn, "/api/issues/start", { keys });

// retryIssue re-arms a failed issue and asks to start it.
export const retryIssue = (fetchFn: Fetch, key: string): Promise<IssueRef> =>
  post<IssueRef>(fetchFn, `/api/issues/${encodeURIComponent(key)}/retry`, {});

export type CloseReply = { closed: IssueRef[] };

// completeIssue moves a parent whose sub-issues are all finished to merged
// (D70); Frankie then closes its provider issue as completed (D71).
export const completeIssue = (fetchFn: Fetch, key: string): Promise<CloseReply> =>
  post<CloseReply>(fetchFn, `/api/issues/${encodeURIComponent(key)}/complete`, {});

// cancelIssue cancels an issue and everything unfinished under it, saying why
// (D70); Frankie then closes their provider issues as not planned (D71).
export async function cancelIssue(fetchFn: Fetch, key: string, reason: string): Promise<CloseReply> {
  const r = reason.trim();
  if (r === "") throw new Error("say why it is canceled");
  return post<CloseReply>(fetchFn, `/api/issues/${encodeURIComponent(key)}/cancel`, { reason: r });
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

// IssueActions is what the issue page offers (M5.1): a button only where it
// does something, otherwise a note saying what the issue waits for.
export type IssueActions = {
  start?: string; // the Start button's label
  retry: boolean;
  complete: boolean;
  cancel: boolean;
  note?: string;
  changeRequest?: string; // the change request a person reviews
};

export function issueActions(
  is: Pick<IssueDetail, "status" | "children" | "start_requested_by" | "start_requested_via" | "spec_proposal" | "change_requests">
    & Partial<Pick<IssueDetail, "spec_rejected_at" | "runs">>,
): IssueActions {
  const finished = isFinished(is.status);
  const out: IssueActions = { retry: is.status === "failed", complete: canComplete(is), cancel: !finished };
  if (finished) return out;
  const requested = is.start_requested_by
    ? `Start requested by ${is.start_requested_by}${is.start_requested_via ? ` (through ${is.start_requested_via})` : ""}`
    : undefined;
  if (is.children.length > 0) {
    // A request on a parent covers everything under it, also sub-issues added
    // later (D62): once requested, there is nothing more to start.
    if (requested) out.note = requested;
    else out.start = "Start all under it";
    return out;
  }
  // A held draft waits for a person even when a request covers it (M5.2):
  // asking again lifts the hold, and the refiner gets the feedback.
  if (is.status === "draft" && is.spec_rejected_at) {
    out.start = "Refine again";
    out.note = "Held until someone asks again";
    return out;
  }
  switch (is.status) {
    case "draft":
    case "ready":
      if (requested) out.note = `${requested}: it starts once its dependencies are done and a slot is free`;
      else out.start = "Start";
      break;
    case "refining":
      out.note = is.spec_proposal ? "The refined spec waits for approval below" : "The refiner is writing its spec";
      break;
    case "running":
      out.note = "A run is in progress";
      break;
    case "queued":
      out.note = "In the merge queue";
      break;
    case "awaiting_human": {
      // A run still going waits for a gate decision or an answer (D91); a
      // finished one left its change request to a person.
      if (is.runs?.some((r) => r.status === "running")) {
        out.note = "Its run waits for a person: decide or answer in the approvals inbox";
        break;
      }
      out.note = "Waiting for a person: review the change request";
      out.changeRequest = is.change_requests.find((c) => c.status === "open")?.url;
      break;
    }
  }
  return out;
}

// specDecidable says whether the page offers to approve or reject the spec
// (D64, D66): a leaf's draft, or a refined spec waiting. A parent's spec is not
// approved; its sub-issues' are.
export function specDecidable(is: Pick<IssueDetail, "status" | "type" | "children" | "spec_proposal">): boolean {
  if (is.children.length > 0 || is.type === "epic") return false;
  return is.status === "draft" || (is.status === "refining" && !!is.spec_proposal);
}

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

// subtree is the issues under key, as tree rows with depth counted from its
// children (0), with the total cost of them all (M5.1).
export function subtree(issues: IssueSummary[], key: string): { rows: TreeRow[]; cost: number } {
  const all = tree(issues);
  const at = all.findIndex((r) => r.issue.key === key);
  if (at < 0) return { rows: [], cost: 0 };
  const base = all[at]!.depth;
  const rows: TreeRow[] = [];
  for (const r of all.slice(at + 1)) {
    if (r.depth <= base) break;
    rows.push({ issue: r.issue, depth: r.depth - base - 1 });
  }
  return { rows, cost: rows.reduce((c, r) => c + r.issue.cost_usd, 0) };
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

// keyPrefix is the project part of an issue key: "gc" of "gc-83".
export const keyPrefix = (key: string): string => key.slice(0, Math.max(0, key.lastIndexOf("-")));

// findProject is the project with a key prefix (the $project of a path, D141).
export const findProject = (projects: Project[], prefix: string): Project | undefined =>
  projects.find((p) => p.key_prefix === prefix);

// ancestors lists the issues above key, the outermost first: the epic and
// story of a task. A parent missing from the list ends the chain.
export function ancestors(issues: IssueSummary[], key: string): IssueSummary[] {
  const byKey = new Map(issues.map((i) => [i.key, i]));
  const out: IssueSummary[] = [];
  const seen = new Set([key]);
  for (let p = byKey.get(key)?.parent; p && !seen.has(p); p = byKey.get(p)?.parent) {
    const i = byKey.get(p);
    if (!i) break;
    seen.add(p);
    out.unshift(i);
  }
  return out;
}
