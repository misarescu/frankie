import { expect, test } from "bun:test";
import type { Approval } from "./approvals";
import { boardRows, boardSummary, graphIssues, waitsFor, type Inbox } from "./board";
import type { RunSummary } from "./events";
import type { IssueSummary, Project } from "./issues";
import type { Question } from "./questions";
import type { ReleaseSummary } from "./releases";
import type { Spec } from "./specs";

const issue = (key: string, over: Partial<IssueSummary> = {}): IssueSummary => ({
  key, number: Number(key.split("-")[1]), type: "task", title: key, status: "ready", priority: 0,
  depends_on: [], labels: [], cost_usd: 0, total_cost_usd: 0, ...over,
});

const project: Project = { id: "p", name: "acme/shop", key_prefix: "gc" };

const issues = [
  issue("gc-1", { type: "epic" }),
  issue("gc-2", { parent: "gc-1", type: "story" }),
  issue("gc-3", { parent: "gc-2", status: "running", cost_usd: 1.5 }),
  issue("gc-4", { parent: "gc-2", status: "merged", cost_usd: 0.5 }),
  issue("gc-5", { status: "merged", cost_usd: 0.25 }),
  issue("gc-6", { depends_on: ["gc-3", "gc-4", "oth-9"] }),
];

const shape = (rows: ReturnType<typeof boardRows>) => rows.map((r) => `${"  ".repeat(r.depth)}${r.issue.key}/${r.children}`);

test("boardRows hides finished issues unless asked for, and counts the sub-issues each row shows", () => {
  expect(shape(boardRows(issues, { finished: false }))).toEqual(["gc-1/1", "  gc-2/1", "    gc-3/0", "gc-6/0"]);
  expect(shape(boardRows(issues, { finished: true }))).toEqual(["gc-1/1", "  gc-2/2", "    gc-3/0", "    gc-4/0", "gc-5/0", "gc-6/0"]);
});

test("boardRows folds a collapsed issue's subtree but keeps the issue and what follows it", () => {
  expect(shape(boardRows(issues, { finished: true, collapsed: new Set(["gc-2"]) }))).toEqual(["gc-1/1", "  gc-2/2", "gc-5/0", "gc-6/0"]);
  expect(shape(boardRows(issues, { finished: true, collapsed: new Set(["gc-1", "gc-2"]) }))).toEqual(["gc-1/1", "gc-5/0", "gc-6/0"]);
  // collapsing a leaf changes nothing
  expect(shape(boardRows(issues, { finished: false, collapsed: new Set(["gc-6"]) }))).toEqual(["gc-1/1", "  gc-2/1", "    gc-3/0", "gc-6/0"]);
});

const inbox = (over: Partial<Inbox> = {}): Inbox => ({ approvals: [], questions: [], specs: [], ...over });
const byKey = new Map(issues.map((i) => [i.key, i]));

test("waitsFor lists the dependencies not finished yet, and keeps ones from elsewhere", () => {
  expect(waitsFor(issues[5]!, byKey, inbox())).toEqual([{ kind: "issue", key: "gc-3" }, { kind: "issue", key: "oth-9" }]);
});

test("waitsFor names a person for what waits in the inbox, linked to its kind", () => {
  const i = issue("gc-7", { status: "running" });
  const got = waitsFor(i, byKey, inbox({
    approvals: [{ issue_key: "gc-7" } as Approval],
    questions: [{ issue_key: "gc-7" } as Question],
    specs: [{ key: "gc-8" } as Spec],
  }));
  expect(got).toEqual([
    { kind: "person", text: "a gate decision", href: "/approvals?kind=gate" },
    { kind: "person", text: "an answer", href: "/approvals?kind=question" },
  ]);
  expect(waitsFor(issue("gc-8", { status: "refining" }), byKey, inbox({ specs: [{ key: "gc-8" } as Spec] })))
    .toEqual([{ kind: "person", text: "spec approval", href: "/approvals?kind=spec" }]);
});

test("waitsFor points an issue left to a person at its open change request", () => {
  const cr = { number: 12, url: "https://example.test/pull/12", status: "open" };
  expect(waitsFor(issue("gc-9", { status: "awaiting_human", change_request: cr }), byKey, inbox()))
    .toEqual([{ kind: "person", text: "review of #12", href: cr.url, external: true }]);
  expect(waitsFor(issue("gc-9", { status: "awaiting_human", change_request: { ...cr, status: "merged" } }), byKey, inbox())).toEqual([]);
});

test("waitsFor says nothing for a finished issue", () => {
  expect(waitsFor(issue("gc-10", { status: "canceled", depends_on: ["gc-3"] }), byKey, inbox())).toEqual([]);
});

test("boardSummary counts running leaves, the project's waiting items, merged issues and all spend", () => {
  const run = (id: string, over: Partial<RunSummary> = {}): RunSummary => ({ ID: id, Status: "running", IssueKey: "", IssueTitle: "", CreatedAt: "", CostUSD: 0, ...over });
  const s = boardSummary(project, [...issues, issue("gc-7", { type: "story", status: "running" }), issue("gc-8", { parent: "gc-7", status: "running" })], inbox({
    approvals: [{ issue_key: "gc-6", run_id: "r1" } as Approval, { issue_key: "release-abc", run_id: "r2" } as Approval,
      { issue_key: "release-def", run_id: "r3" } as Approval, { issue_key: "oth-1", run_id: "r4" } as Approval],
    questions: [{ issue_key: "gc-3" } as Question, { issue_key: "oth-2" } as Question],
    specs: [{ key: "gc-2", project: "acme/shop" } as Spec, { key: "oth-3", project: "acme/other" } as Spec],
  }), {
    runs: [run("r2", { ReleaseID: "rel-1" }), run("r3", { ReleaseID: "rel-other" })],
    releases: [{ id: "rel-1" } as ReleaseSummary],
  });
  // running: gc-3 and gc-8, not the story gc-7 above gc-8
  expect(s).toEqual({ running: 2, waiting: 4, merged: 2, spent: 2.25 });
});

test("boardSummary counts nothing waiting before the inbox loads", () => {
  expect(boardSummary(project, issues, undefined, undefined).waiting).toBe(0);
});

test("graphIssues keeps a finished issue only while an unfinished one waits for it", () => {
  const keys = (is: IssueSummary[]) => is.map((i) => i.key);
  expect(keys(graphIssues(issues, false))).toEqual(["gc-1", "gc-2", "gc-3", "gc-4", "gc-6"]);
  expect(keys(graphIssues(issues, true))).toHaveLength(6);
});
