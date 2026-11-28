import { expect, test } from "bun:test";
import {
  ancestors, canComplete, cancelIssue, findProject, keyPrefix, completeIssue, dependencyGraph, isFinished, issueActions, leafStatuses, specDecidable, progress, progressText, retryIssue, startIssues, subtree, tree,
  type IssueDetail, type IssueRef, type IssueSummary, detailWithTotal, summaryWithTotal,
} from "./issues";

const issue = (key: string, over: Partial<IssueSummary> = {}): IssueSummary => ({
  key, number: Number(key.split("-")[1]), type: "task", title: key, status: "ready", priority: 0,
  depends_on: [], labels: [], cost_usd: 0, total_cost_usd: 0, ...over,
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

test("M5.1: the issue page offers Start only where it does something", () => {
  const ref = (status: string): IssueRef => ({ key: "k", type: "task", title: "t", status });
  const leaf = { status: "ready", children: [] as IssueRef[], change_requests: [] as IssueDetail["change_requests"] };
  expect(issueActions(leaf)).toEqual({ start: "Start", retry: false, complete: false, cancel: true });
  expect(issueActions({ ...leaf, start_requested_by: "Mihai", start_requested_via: "gc-11" }).note)
    .toBe("Start requested by Mihai (through gc-11): it starts once its dependencies are done and a slot is free");
  expect(issueActions({ ...leaf, status: "running" })).toEqual({ retry: false, complete: false, cancel: true, note: "A run is in progress" });
  const waiting = issueActions({ ...leaf, status: "awaiting_human",
    change_requests: [{ repo: "o/r", number: 3, url: "u3", status: "closed" }, { repo: "o/r", number: 4, url: "u4", status: "open" }] });
  expect([waiting.start, waiting.note, waiting.changeRequest]).toEqual([undefined, "Waiting for a person: review the change request", "u4"]);
  // M6 (D91): inside a run, the issue waits for a gate decision or an answer.
  const inRun = issueActions({ ...leaf, status: "awaiting_human", runs: [{ id: "r", status: "running", workflow: "w", created_at: "", cost_usd: 0 }] });
  expect([inRun.note, inRun.changeRequest]).toEqual(["Its run waits for a person: decide or answer in the approvals inbox", undefined]);
  expect(issueActions({ ...leaf, status: "failed" })).toEqual({ retry: true, complete: false, cancel: true });
  expect(issueActions({ ...leaf, status: "refining", spec_proposal: { spec: "s", acceptance_criteria: [] } }).note).toContain("waits for approval");
  expect(issueActions({ ...leaf, status: "merged" })).toEqual({ retry: false, complete: false, cancel: false });

  const parent = { ...leaf, status: "draft", children: [ref("running"), ref("merged")] };
  expect(issueActions(parent).start).toBe("Start all under it");
  expect(issueActions({ ...parent, start_requested_by: "Mihai" })).toEqual({ retry: false, complete: false, cancel: true, note: "Start requested by Mihai" });
  expect(issueActions({ ...parent, children: [ref("merged")] })).toMatchObject({ complete: true, cancel: true });
});

test("M5.2: a held draft offers to refine again, also under a parent's request", () => {
  const held = { status: "draft", children: [] as IssueRef[], change_requests: [] as IssueDetail["change_requests"],
    spec_rejected_at: "2026-11-15T02:36:00Z" as string | undefined };
  expect(issueActions(held)).toEqual({ start: "Refine again", retry: false, complete: false, cancel: true, note: "Held until someone asks again" });
  expect(issueActions({ ...held, start_requested_by: "Mihai", start_requested_via: "gc-13" }).start).toBe("Refine again");
  expect(issueActions({ ...held, spec_rejected_at: undefined, start_requested_by: "Mihai" }).start).toBeUndefined();
});

test("M5.1: a parent's spec is not approved", () => {
  const base = { status: "draft", type: "task", children: [] as IssueRef[] };
  expect(specDecidable(base)).toBe(true);
  expect(specDecidable({ ...base, type: "epic" })).toBe(false);
  expect(specDecidable({ ...base, type: "story", children: [{ key: "k", type: "task", title: "t", status: "ready" }] })).toBe(false);
  expect(specDecidable({ ...base, status: "refining" })).toBe(false);
  expect(specDecidable({ ...base, status: "refining", spec_proposal: { spec: "s", acceptance_criteria: [] } })).toBe(true);
  expect(specDecidable({ ...base, status: "ready" })).toBe(false);
});

test("M5.1: a parent's page lists everything under it, with the total cost", () => {
  const issues = [
    issue("gc-1", { type: "epic", status: "draft" }),
    issue("gc-2", { parent: "gc-1", type: "story", status: "draft" }),
    issue("gc-3", { parent: "gc-2", status: "merged", cost_usd: 0.25 }),
    issue("gc-4", { parent: "gc-2", status: "running", cost_usd: 0.5 }),
    issue("gc-5", { parent: "gc-1", type: "bug", status: "awaiting_human", cost_usd: 0.25 }),
    issue("gc-6", { status: "ready", cost_usd: 9 }), // another tree
  ];
  const all = subtree(issues, "gc-1");
  expect(all.rows.map((r) => `${r.depth}:${r.issue.key}`)).toEqual(["0:gc-2", "1:gc-3", "1:gc-4", "0:gc-5"]);
  expect(all.cost).toBe(1);
  expect(subtree(issues, "gc-2").rows.map((r) => `${r.depth}:${r.issue.key}`)).toEqual(["0:gc-3", "0:gc-4"]);
  expect(subtree(issues, "gc-3").rows).toEqual([]);
  expect(subtree(issues, "gc-99")).toEqual({ rows: [], cost: 0 });
});


// M9 (D141): paths name a project by key prefix; breadcrumbs walk up the tree.
test("keyPrefix and findProject", () => {
  expect(keyPrefix("gc-83")).toBe("gc");
  expect(keyPrefix("frk-dummy-2")).toBe("frk-dummy");
  expect(keyPrefix("nokey")).toBe("");
  const projects = [{ id: "1", name: "misarescu/Goodreads-Clone", key_prefix: "gc" }, { id: "2", name: "frankie", key_prefix: "frk" }];
  expect(findProject(projects, "frk")?.name).toBe("frankie");
  expect(findProject(projects, "nope")).toBeUndefined();
});

test("ancestors lists the parents outermost first, and stops at a missing or looping parent", () => {
  const all = [
    issue("gc-80", { type: "epic" }),
    issue("gc-81", { type: "story", parent: "gc-80" }),
    issue("gc-83", { parent: "gc-81" }),
    issue("gc-90", { parent: "gc-99" }),
    issue("gc-91", { parent: "gc-92" }),
    issue("gc-92", { parent: "gc-91" }),
  ];
  expect(ancestors(all, "gc-83").map((i) => i.key)).toEqual(["gc-80", "gc-81"]);
  expect(ancestors(all, "gc-80")).toEqual([]);
  expect(ancestors(all, "gc-90")).toEqual([]);
  expect(ancestors(all, "gc-91").map((i) => i.key)).toEqual(["gc-92"]);
  expect(ancestors(all, "nope")).toEqual([]);
});

// D142: an API without total_cost_usd (a control plane from before M9).
test("an issue without total_cost_usd totals its own cost", () => {
  const s = { key: "gc-7", cost_usd: 0.25 } as unknown as IssueSummary;
  expect(summaryWithTotal(s).total_cost_usd).toBe(0.25);
  const d = { key: "gc-7", usage: { cost_usd: 0.5, input_tokens: 0, output_tokens: 0 } } as unknown as IssueDetail;
  expect(detailWithTotal(d).total_cost_usd).toBe(0.5);
});

test("an issue's total_cost_usd from the API is kept", () => {
  const s = { key: "gc-7", cost_usd: 0.25, total_cost_usd: 3 } as unknown as IssueSummary;
  expect(summaryWithTotal(s).total_cost_usd).toBe(3);
  const d = { key: "gc-7", usage: { cost_usd: 0.5, input_tokens: 0, output_tokens: 0 }, total_cost_usd: 4 } as unknown as IssueDetail;
  expect(detailWithTotal(d).total_cost_usd).toBe(4);
});
