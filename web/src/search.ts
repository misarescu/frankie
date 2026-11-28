// The search params of each route (D141), parsed from whatever the URL holds:
// an unknown value is dropped, so a hand-edited URL shows the default instead
// of breaking the page. The router passes these as validateSearch.

import type { ApprovalKind, BoardView, IssueTab, RunsFilter } from "./paths";

type Raw = Record<string, unknown>;

const oneOf = <T extends string>(v: unknown, allowed: readonly T[]): T | undefined =>
  typeof v === "string" && (allowed as readonly string[]).includes(v) ? (v as T) : undefined;

export type BoardSearch = { view?: BoardView; finished?: 1 };
export const boardSearch = (s: Raw): BoardSearch => ({
  view: oneOf(s.view, ["list", "graph"] as const),
  finished: s.finished === 1 || s.finished === "1" || s.finished === true ? 1 : undefined,
});

export type RunsSearch = { status?: RunsFilter };
export const runsSearch = (s: Raw): RunsSearch => ({ status: oneOf(s.status, ["active", "failed"] as const) });

export type IssueSearch = { tab?: IssueTab };
export const issueSearch = (s: Raw): IssueSearch => ({ tab: oneOf(s.tab, ["overview", "runs", "issues", "activity"] as const) });

export type RunSearch = { step?: string };
export const runSearch = (s: Raw): RunSearch => ({
  step: typeof s.step === "string" && s.step !== "" ? s.step : typeof s.step === "number" ? String(s.step) : undefined,
});

export type ApprovalsSearch = { kind?: ApprovalKind };
export const approvalsSearch = (s: Raw): ApprovalsSearch => ({ kind: oneOf(s.kind, ["gate", "question", "spec"] as const) });
