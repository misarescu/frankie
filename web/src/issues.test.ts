import { expect, test } from "bun:test";
import {
  canComplete, cancelIssue, completeIssue, dependencyGraph, isFinished, leafStatuses, progress, progressText, retryIssue, shortID, startIssues, tree,
  type IssueRef, type IssueSummary,
} from "./issues";

const issue = (key: string, over: Partial<IssueSummary> = {}): IssueSummary => ({
  key, number: Number(key.split("-")[1]), type: "task", title: key, status: "ready", priority: 0,
  depends_on: [], labels: [], cost_usd: 0, ...over,
});

test("tree puts children under their parents in the API's order", () => {
  const rows = tree([
    issue("gc-1", { type: "epic" }),
    issue("gc-5"),
    issue("gc-2", { parent: "gc-1", type: "story" }),
    issue("gc-3", { parent: "gc-2" }),
    issue("gc-4", { parent: "gc-1" }),
    issue("gc-6", { parent: "gc-99" }), // parent not listed: a root
  ]);
  expect(rows.map((r) => `${"  ".repeat(r.depth)}${r.issue.key}`)).toEqual([
    "gc-1", "  gc-2", "    gc-3", "  gc-4", "gc-5", "gc-6",
  ]);
});

test("tree hides finished issues but keeps parents of kept ones", () => {
  const rows = tree([
    issue("gc-1", { type: "epic", status: "draft" }),
    issue("gc-2", { parent: "gc-1", status: "merged" }),
    issue("gc-3", { parent: "gc-1", status: "running" }),
    issue("gc-4", { type: "epic", status: "merged" }),
    issue("gc-5", { parent: "gc-4", status: "merged" }),
    issue("gc-6", { type: "epic", status: "merged" }),
    issue("gc-7", { parent: "gc-6", status: "ready" }),
  ], (i) => isFinished(i.status));
  expect(rows.map((r) => r.issue.key)).toEqual(["gc-1", "gc-3", "gc-6", "gc-7"]);
});

test("dependencyGraph layers issues after what they wait for", () => {
  const g = dependencyGraph([
    issue("gc-1"),
    issue("gc-2", { depends_on: ["gc-1"] }),
    issue("gc-3", { depends_on: ["gc-1"] }),
    issue("gc-4", { depends_on: ["gc-2", "gc-3"] }),
    issue("gc-5"), // no dependency: not drawn
    issue("gc-6", { depends_on: ["gc-404"] }), // waits for an issue not listed: not drawn
  ]);
  expect(g.nodes.map((n) => `${n.key}@${n.layer}.${n.row}`)).toEqual(["gc-1@0.0", "gc-2@1.0", "gc-3@1.1", "gc-4@2.0"]);
  expect(g.edges).toEqual([
    { from: "gc-2", to: "gc-1" }, { from: "gc-3", to: "gc-1" }, { from: "gc-4", to: "gc-2" }, { from: "gc-4", to: "gc-3" },
  ]);
});

test("dependencyGraph survives a cycle", () => {
  const g = dependencyGraph([issue("gc-1", { depends_on: ["gc-2"] }), issue("gc-2", { depends_on: ["gc-1"] })]);
  expect(g.nodes).toHaveLength(2);
});

test("startIssues posts the keys and returns the plan", async () => {
  let body = "";
  const reply = { requested: ["gc-1"], starting: ["gc-2"], waiting: [{ key: "gc-3", reason: "waits for gc-2" }], overlaps: [] };
  const fetchFn = async (_url: string, init?: RequestInit) => {
    body = String(init?.body);
    return Response.json(reply, { status: 202 });
  };
  expect(await startIssues(fetchFn, ["gc-1"])).toEqual(reply);
  expect(JSON.parse(body)).toEqual({ keys: ["gc-1"] });
});

test("retryIssue reports the server's reason", async () => {
  const conflict = async () => new Response("gc-1 is running; only a failed issue can be retried\n", { status: 409 });
  await expect(retryIssue(conflict, "gc-1")).rejects.toThrow("only a failed issue");
});

test("M5.1: shortID keeps the end of a run ID", () => {
  expect(shortID("0198f2a4-1111-7000-8000-00000000abcd")).toBe("0000abcd");
});

test("M5.1: startIssues and retryIssue send the person's name as by", async () => {
  const bodies: unknown[] = [];
  const fetchFn = async (_url: string, init?: RequestInit) => {
    bodies.push(JSON.parse(String(init?.body)));
    return Response.json({}, { status: 202 });
  };
  await startIssues(fetchFn, ["gc-1"], "Ana");
  await retryIssue(fetchFn, "gc-1", "Ana");
  expect(bodies).toEqual([{ keys: ["gc-1"], by: "Ana" }, { by: "Ana" }]);
});

test("M5.1: a parent's progress counts merged, canceled and all", () => {
  const p = progress(["merged", "released", "canceled", "running", "draft"]);
  expect(p).toEqual({ merged: 2, canceled: 1, total: 5 });
  expect(progressText(p)).toBe("2 of 5 merged, 1 canceled");
  expect(progressText(progress(["merged", "ready"]))).toBe("1 of 2 merged");
});

test("M5.1: the issue list counts the leaves under a parent", () => {
  const issues = [
    issue("gc-1", { type: "epic", status: "draft" }),
    issue("gc-2", { parent: "gc-1", type: "story", status: "draft" }),
    issue("gc-3", { parent: "gc-2", status: "merged" }),
    issue("gc-4", { parent: "gc-2", status: "running" }),
    issue("gc-5", { parent: "gc-1", type: "bug", status: "canceled" }),
  ];
  expect(leafStatuses(issues, "gc-1").sort()).toEqual(["canceled", "merged", "running"]);
  expect(leafStatuses(issues, "gc-2").sort()).toEqual(["merged", "running"]);
  expect(leafStatuses(issues, "gc-3")).toEqual([]);
});

test("M5.1: Complete is offered exactly when the API would accept it", () => {
  const ref = (status: string): IssueRef => ({ key: "k", type: "task", title: "t", status });
  expect(canComplete({ status: "draft", children: [ref("merged"), ref("canceled")] })).toBe(true);
  expect(canComplete({ status: "ready", children: [ref("released")] })).toBe(true);
  expect(canComplete({ status: "draft", children: [ref("merged"), ref("awaiting_human")] })).toBe(false);
  expect(canComplete({ status: "draft", children: [ref("canceled")] })).toBe(false); // cancel it instead
  expect(canComplete({ status: "draft", children: [] })).toBe(false); // a leaf
  expect(canComplete({ status: "merged", children: [ref("merged")] })).toBe(false);
});

test("M5.1: complete and cancel post to the issue, with the name and the reason", async () => {
  const calls: { url: string; body: unknown }[] = [];
  const fetchFn = async (url: string, init?: RequestInit) => {
    calls.push({ url, body: JSON.parse(String(init?.body)) });
    return Response.json({ closed: [{ key: "gc-7", type: "epic", title: "t", status: "merged" }] });
  };
  expect((await completeIssue(fetchFn, "gc-7", "Ana")).closed[0]!.status).toBe("merged");
  await cancelIssue(fetchFn, "gc-1", "  its change request was closed ", "Ana");
  expect(calls).toEqual([
    { url: "/api/issues/gc-7/complete", body: { by: "Ana" } },
    { url: "/api/issues/gc-1/cancel", body: { reason: "its change request was closed", by: "Ana" } },
  ]);
  await expect(cancelIssue(fetchFn, "gc-1", " ")).rejects.toThrow("say why");
  expect(calls).toHaveLength(2);
  const refused = async () => new Response("gc-1 has a run in progress (run x); abort its step or decide its gate first\n", { status: 409 });
  await expect(cancelIssue(refused, "gc-1", "stop")).rejects.toThrow("run in progress");
});

