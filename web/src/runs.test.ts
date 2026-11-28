import { expect, test } from "bun:test";
import type { RunSummary } from "./events";
import { filterRuns, isActiveRun, projectRuns } from "./runs";

const run = (id: string, over: Partial<RunSummary> = {}): RunSummary => ({
  ID: id, Status: "succeeded", IssueKey: "", IssueTitle: "", CreatedAt: "2026-11-28T10:00:00Z", CostUSD: 0, ...over,
});

const runs = [
  run("a", { IssueKey: "gc-1", CreatedAt: "2026-11-28T09:00:00Z" }),
  run("b", { IssueKey: "gcx-1", CreatedAt: "2026-11-28T12:00:00Z" }), // another project whose prefix starts the same
  run("c", { ReleaseID: "rel-1", CreatedAt: "2026-11-28T11:00:00Z", Status: "running" }),
  run("d", { ReleaseID: "rel-9", CreatedAt: "2026-11-28T13:00:00Z" }),
  run("e", { IssueKey: "gc-12", CreatedAt: "2026-11-28T10:00:00Z", Status: "failed" }),
];

test("projectRuns keeps the project's issue runs and its releases' runs, newest first", () => {
  expect(projectRuns(runs, "gc", new Set(["rel-1"])).map((r) => r.ID)).toEqual(["c", "e", "a"]);
  expect(projectRuns(runs, "gc", new Set()).map((r) => r.ID)).toEqual(["e", "a"]);
});

test("filterRuns keeps all, the active ones or the failed ones", () => {
  const mine = projectRuns(runs, "gc", new Set(["rel-1"]));
  expect(filterRuns(mine, undefined).map((r) => r.ID)).toEqual(["c", "e", "a"]);
  expect(filterRuns(mine, "active").map((r) => r.ID)).toEqual(["c"]);
  expect(filterRuns(mine, "failed").map((r) => r.ID)).toEqual(["e"]);
});

test("isActiveRun: pending and running move on; finished runs do not", () => {
  expect(["pending", "running", "succeeded", "failed", "canceled"].map(isActiveRun)).toEqual([true, true, false, false, false]);
});
