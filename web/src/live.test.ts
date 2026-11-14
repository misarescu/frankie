import { expect, test } from "bun:test";
import type { RunEvent } from "./events";
import { clip, duration, keepLatest, latestRun, watch, type LiveRunRef } from "./live";

const ev = (seq: number, type: string): RunEvent => ({ seq, time: "", type, payload: {} });

test("M5.1: keeps the latest agent activity and drops the rest", () => {
  let list: RunEvent[] = [];
  for (const [i, t] of ["assistant_message", "token_usage", "tool_call", "tool_result", "question", "state_change"].entries()) {
    list = keepLatest(list, ev(i, t), 2);
  }
  expect(list.map((e) => e.type)).toEqual(["tool_call", "question"]);
});

test("M5.1: says how long a step took or has been running", () => {
  const now = new Date("2026-11-14T10:10:00Z");
  expect(duration(undefined, undefined, now)).toBe("");
  expect(duration("2026-11-14T10:09:58Z", undefined, now)).toBe("2s");
  expect(duration("2026-11-14T10:00:00Z", "2026-11-14T10:03:05Z", now)).toBe("3m 05s");
  expect(duration("2026-11-14T08:00:00Z", "2026-11-14T09:02:00Z", now)).toBe("1h 02m");
  expect(duration("2026-11-14T10:10:05Z", undefined, now)).toBe("0s"); // clock skew
});

test("M5.1: a run that finishes stays watched; new ones join, oldest drop first", () => {
  const run = (id: string, created_at: string, status = "running"): LiveRunRef =>
    ({ id, status, workflow: "default-build@1", created_at, issue_key: "gc-1", issue_title: "t" });
  const a = run("a", "2026-11-14T10:00:00Z");
  const b = run("b", "2026-11-14T10:05:00Z");
  let w = watch([], [a]);
  w = watch(w, [b]); // a finished: no longer live, still watched
  expect(w.map((r) => r.id)).toEqual(["a", "b"]);
  w = watch(w, [{ ...b, status: "pending" }]);
  expect(w.find((r) => r.id === "b")?.status).toBe("pending");
  const c = run("c", "2026-11-14T10:06:00Z");
  expect(watch(w, [c], 2).map((r) => r.id)).toEqual(["b", "c"]);
});

test("M5.1: clips long text", () => {
  expect(clip("short")).toBe("short");
  expect(clip("abcdef ghij", 7)).toBe("abcdef …");
});

test("M5.1: an issue that does the work keeps showing its latest run; a parent shows none", () => {
  const runs = [{ id: "r2", status: "succeeded", workflow: "default-build@1", created_at: "2026-11-15T01:10:11Z" },
    { id: "r1", status: "failed", workflow: "default-build@1", created_at: "2026-11-15T00:10:11Z" }];
  expect(latestRun({ key: "gc-12", title: "Rate a single book", children: [], runs })).toEqual(
    { id: "r2", status: "succeeded", workflow: "default-build@1", created_at: "2026-11-15T01:10:11Z", issue_key: "gc-12", issue_title: "Rate a single book" });
  expect(latestRun({ key: "gc-12", title: "t", children: [], runs: [] })).toBeUndefined();
  expect(latestRun({ key: "gc-11", title: "t", children: [{}], runs })).toBeUndefined();
});

