import { expect, test } from "bun:test";
import { paths } from "./paths";

// D141's URLs; the default tab and view stay out of the URL.
test("paths", () => {
  expect(paths.board("gc")).toBe("/projects/gc/board");
  expect(paths.board("gc", { view: "graph", finished: 1 })).toBe("/projects/gc/board?view=graph&finished=1");
  expect(paths.board("gc", { view: "list" })).toBe("/projects/gc/board");
  expect(paths.runs("frk")).toBe("/projects/frk/runs");
  expect(paths.runs("frk", "failed")).toBe("/projects/frk/runs?status=failed");
  expect(paths.releases("gc")).toBe("/projects/gc/releases");
  expect(paths.issue("gc-83")).toBe("/issues/gc-83");
  expect(paths.issue("gc-83", "overview")).toBe("/issues/gc-83");
  expect(paths.issue("gc-83", "runs")).toBe("/issues/gc-83?tab=runs");
  expect(paths.run("0198f2a4-2222-7000-8000-000000000002")).toBe("/runs/0198f2a4-2222-7000-8000-000000000002");
  expect(paths.run("r1", "s 1")).toBe("/runs/r1?step=s%201");
  expect(paths.release("rel-1")).toBe("/releases/rel-1");
  expect(paths.approvals()).toBe("/approvals");
  expect(paths.approvals("spec")).toBe("/approvals?kind=spec");
});
