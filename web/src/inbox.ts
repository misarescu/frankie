// The approvals inbox (M9): what waits for a person across every project, as
// one list of items of three kinds. An agent is paused until its question is
// answered, so questions come first; then human gates, oldest first; then
// refined specs. A gate before a release stage (D139) belongs to a release,
// not an issue: the API names it release-<sha7>.

import type { Approval } from "./approvals";
import type { RunSummary } from "./events";
import type { ApprovalKind } from "./paths";
import type { Question } from "./questions";
import type { ReleaseSummary } from "./releases";
import type { Spec } from "./specs";

export type InboxItem =
  | { kind: "question"; id: string; question: Question }
  | { kind: "gate"; id: string; approval: Approval }
  | { kind: "spec"; id: string; spec: Spec };

const byCreated = <T extends { created_at: string }>(a: T, b: T) => a.created_at.localeCompare(b.created_at);

export function inboxItems(questions: Question[], gates: Approval[], specs: Spec[]): InboxItem[] {
  return [
    ...[...questions].sort(byCreated).map((q): InboxItem => ({ kind: "question", id: q.id, question: q })),
    ...[...gates].sort(byCreated).map((a): InboxItem => ({ kind: "gate", id: a.step_run_id, approval: a })),
    ...specs.map((s): InboxItem => ({ kind: "spec", id: s.key, spec: s })),
  ];
}

export type InboxCounts = Record<ApprovalKind | "all", number>;

export function inboxCounts(items: InboxItem[]): InboxCounts {
  const c: InboxCounts = { all: items.length, gate: 0, question: 0, spec: 0 };
  for (const i of items) c[i.kind]++;
  return c;
}

// ofKind is the inbox filtered by the kind in the URL; every kind when absent.
export const ofKind = (items: InboxItem[], kind?: ApprovalKind): InboxItem[] => (kind ? items.filter((i) => i.kind === kind) : items);

// releaseGateSHA is the short commit a release stage's gate names, or
// undefined for a gate on an issue.
export function releaseGateSHA(a: Pick<Approval, "issue_key">): string | undefined {
  return /^release-([0-9a-f]{7})$/.exec(a.issue_key)?.[1];
}

// releaseOf finds the release a gate belongs to: the release its run belongs
// to when the run is in the recent runs, else the release whose commit and
// repository the gate names.
export function releaseOf(
  a: Pick<Approval, "run_id" | "issue_key" | "issue_title">,
  runs: Pick<RunSummary, "ID" | "ReleaseID">[],
  releases: Pick<ReleaseSummary, "id" | "sha" | "repo">[],
): string | undefined {
  const sha = releaseGateSHA(a);
  if (!sha) return undefined;
  const run = runs.find((r) => r.ID === a.run_id);
  if (run?.ReleaseID) return run.ReleaseID;
  return releases.find((r) => r.sha.startsWith(sha) && a.issue_title.endsWith(` of ${r.repo}`))?.id;
}

// isProtected: the gate holds a change to protected paths (invariant 6).
export const isProtected = (a: Pick<Approval, "reason">): boolean => /^protected paths/i.test(a.reason);

// gateTitle says what a gate asks, in a line.
export function gateTitle(a: Pick<Approval, "issue_key" | "step" | "reason">, releaseName?: string): string {
  const sha = releaseGateSHA(a);
  if (sha) return `Deploy ${releaseName ?? sha} to ${a.step}`;
  if (isProtected(a)) return `Protected paths changed in ${a.issue_key}`;
  return `The ${a.step} gate of ${a.issue_key}`;
}

const emptyText: Record<ApprovalKind | "all", { title: string; body: string }> = {
  all: { title: "Nothing waits for you", body: "Human gates, agents' questions and refined specs show up here, and as a count in the sidebar." },
  gate: { title: "No gates wait for a decision", body: "A run stops at a human gate (protected changes, a merge approval, a bug-fix decision or a release stage) and waits here." },
  question: { title: "No agent waits for an answer", body: "An agent that asks with the ask-human tool pauses until someone answers here." },
  spec: { title: "No refined spec waits for approval", body: "With spec approval required, the refiner's spec of an issue waits here before the issue runs." },
};

// emptyInbox is what an empty inbox (or one kind of it) says.
export const emptyInbox = (kind?: ApprovalKind) => emptyText[kind ?? "all"];
