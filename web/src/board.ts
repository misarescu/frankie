// The board (M5, M9): which rows of a project's issue tree show, what each
// issue waits for, and the summary strip over them.

import type { Approval } from "./approvals";
import type { RunSummary } from "./events";
import { isFinished, keyPrefix, tree, type IssueSummary, type Project } from "./issues";
import { paths } from "./paths";
import type { Question } from "./questions";
import type { ReleaseSummary } from "./releases";
import type { Spec } from "./specs";

// BoardRow is one row of the board: an issue at its depth, and how many of
// its sub-issues show (the row's toggle folds them).
export type BoardRow = { issue: IssueSummary; depth: number; children: number };

// boardRows is the tree the board shows: finished issues hidden unless asked
// for (a finished parent stays while something under it shows), and the
// subtrees of collapsed issues folded away.
export function boardRows(issues: IssueSummary[], opts: { finished: boolean; collapsed?: ReadonlySet<string> }): BoardRow[] {
  const rows = tree(issues, (i) => !opts.finished && isFinished(i.status));
  const out: BoardRow[] = [];
  let foldedBelow = Infinity; // the depth of the collapsed row whose subtree is skipped
  rows.forEach((r, n) => {
    let children = 0;
    for (let m = n + 1; m < rows.length && rows[m]!.depth > r.depth; m++) if (rows[m]!.depth === r.depth + 1) children++;
    if (r.depth > foldedBelow) return;
    foldedBelow = opts.collapsed?.has(r.issue.key) && children > 0 ? r.depth : Infinity;
    out.push({ issue: r.issue, depth: r.depth, children });
  });
  return out;
}

// Inbox is what waits for a person across projects: the approvals inbox.
export type Inbox = { approvals: Approval[]; questions: Question[]; specs: Spec[] };

// WaitPart is one thing an issue waits for: an issue that must finish first,
// or a person, linked to where they act.
export type WaitPart = { kind: "issue"; key: string } | { kind: "person"; text: string; href: string; external?: boolean };

// waitsFor says what keeps an issue from moving on: its dependencies not
// finished yet, then a person when the inbox holds something for it (a gate,
// a question, a refined spec) or its run left a change request to review.
export function waitsFor(i: IssueSummary, byKey: ReadonlyMap<string, IssueSummary>, inbox: Inbox | undefined): WaitPart[] {
  if (isFinished(i.status)) return [];
  const out: WaitPart[] = i.depends_on
    .filter((d) => { const dep = byKey.get(d); return !dep || !isFinished(dep.status); })
    .map((key) => ({ kind: "issue", key }));
  if (inbox?.approvals.some((a) => a.issue_key === i.key)) out.push({ kind: "person", text: "a gate decision", href: paths.approvals("gate") });
  if (inbox?.questions.some((q) => q.issue_key === i.key)) out.push({ kind: "person", text: "an answer", href: paths.approvals("question") });
  if (inbox?.specs.some((s) => s.key === i.key)) out.push({ kind: "person", text: "spec approval", href: paths.approvals("spec") });
  const waiting = out.some((p) => p.kind === "person");
  if (!waiting && i.status === "awaiting_human" && i.change_request?.status === "open") {
    out.push({ kind: "person", text: `review of #${i.change_request.number}`, href: i.change_request.url, external: true });
  }
  return out;
}

export type BoardSummary = { running: number; waiting: number; merged: number; spent: number };

// boardSummary is the strip above the board: the issues with a run going
// (leaves: a parent is running while any issue under it is), what waits for
// a person in the project (its issues' gates, questions and specs, and its
// releases' gates), the issues merged, and what every issue spent.
export function boardSummary(project: Project, issues: IssueSummary[], inbox: Inbox | undefined,
  releaseRuns: { runs: RunSummary[]; releases: ReleaseSummary[] } | undefined): BoardSummary {
  const parents = new Set(issues.flatMap((i) => (i.parent ? [i.parent] : [])));
  const mine = (key: string) => keyPrefix(key) === project.key_prefix;
  const releaseIDs = new Set(releaseRuns?.releases.map((r) => r.id));
  const releaseRunIDs = new Set(releaseRuns?.runs.filter((r) => r.ReleaseID && releaseIDs.has(r.ReleaseID)).map((r) => r.ID));
  const waiting = inbox
    ? inbox.approvals.filter((a) => mine(a.issue_key) || releaseRunIDs.has(a.run_id)).length +
      inbox.questions.filter((q) => mine(q.issue_key)).length +
      inbox.specs.filter((s) => s.project === project.name).length
    : 0;
  return {
    running: issues.filter((i) => i.status === "running" && !parents.has(i.key)).length,
    waiting,
    merged: issues.filter((i) => i.status === "merged" || i.status === "released").length,
    spent: issues.reduce((c, i) => c + i.cost_usd, 0),
  };
}

// graphIssues is what the dependency graph draws: the unfinished issues, and
// finished ones only while an unfinished issue waits for them (or all, when
// finished issues are asked for).
export function graphIssues(issues: IssueSummary[], finished: boolean): IssueSummary[] {
  if (finished) return issues;
  return issues.filter((i) => !isFinished(i.status) || issues.some((o) => o.depends_on.includes(i.key) && !isFinished(o.status)));
}
