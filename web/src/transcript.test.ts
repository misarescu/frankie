import { expect, test } from "bun:test";
import type { RunEvent } from "./events";
import type { Step } from "./steps";
import { atBottom, connection, diffLine, isShell, loopBack, releaseRunCrumbs, rows, RUN_SECTION, runCrumbs, selectedStepID, stepEvents } from "./transcript";

const step = (id: string, step_id: string, status: string, kind = "agent", attempt = 1): Step =>
  ({ id, step_id, attempt, kind, status, cost_usd: 0, input_tokens: 0, output_tokens: 0 });

let seq = 0;
const ev = (type: string, payload: Record<string, unknown> = {}, step_run_id?: string): RunEvent =>
  ({ seq: ++seq, time: "2026-11-28T10:00:00Z", type, payload, ...(step_run_id ? { step_run_id } : {}) });

test("selects the step the URL names, else the running one, else the last that ran", () => {
  const tl = [step("a", "scope", "succeeded"), step("b", "implement", "running"), step("plan-checks", "checks", "not started", "gate")];
  expect(selectedStepID(tl, "a", false)).toBe("a");
  expect(selectedStepID(tl, "plan-checks", false)).toBe("plan-checks"); // a step not reached yet can be opened
  expect(selectedStepID(tl, undefined, false)).toBe("b");
  expect(selectedStepID(tl, "nope", false)).toBe("b"); // an id the run does not have falls back
  const parallel = [step("r1", "review-security", "running"), step("r2", "review-style", "running")];
  expect(selectedStepID(parallel, undefined, false)).toBe("r2");
  const ended = [step("a", "scope", "succeeded"), step("c", "checks", "failed", "gate"), step("plan-x", "x", "skipped")];
  expect(selectedStepID(ended, undefined, false)).toBe("c");
  expect(selectedStepID([], undefined, false)).toBeUndefined();
  expect(selectedStepID([], undefined, true)).toBe(RUN_SECTION);
  expect(selectedStepID(tl, RUN_SECTION, true)).toBe(RUN_SECTION);
  expect(selectedStepID(tl, RUN_SECTION, false)).toBe("b"); // no run events: nothing to show there
});

test("a step's events, or the run's own", () => {
  const events = [ev("state_change", {}, undefined), ev("assistant_message", {}, "a"), ev("done", {}, "b"), ev("state_change")];
  expect(stepEvents(events, "a").map((e) => e.type)).toEqual(["assistant_message"]);
  expect(stepEvents(events, RUN_SECTION)).toHaveLength(2);
  expect(stepEvents(events, "zzz")).toEqual([]);
});

test("the step a failed gate sent the run back to says so", () => {
  const tl = [step("i1", "implement", "succeeded"), step("c1", "checks", "failed", "gate"), step("plan-x", "x", "skipped"),
    step("i2", "implement", "succeeded", "agent", 2), step("c2", "checks", "succeeded", "gate", 2)];
  expect(loopBack(tl, 0)).toBeUndefined();
  expect(loopBack(tl, 3)).toBe("looped back after checks failed");
  expect(loopBack(tl, 4)).toBeUndefined(); // runs again after the loop; its attempt says so
  expect(loopBack([step("i2", "implement", "running", "agent", 2)], 0)).toBeUndefined();
});

test("joins tool calls to their results by id, else in order", () => {
  const byID = rows([
    ev("tool_call", { id: "t1", name: "Read" }), ev("tool_call", { id: "t2", name: "Bash" }),
    ev("command_output", { tool_call_id: "t2", output: "ok" }), ev("tool_result", { tool_call_id: "t1", content: "file" }),
  ]);
  expect(byID.map((r) => r.kind)).toEqual(["tool", "tool"]);
  const [read, bash] = byID as Extract<(typeof byID)[number], { kind: "tool" }>[];
  expect(read!.result?.payload.content).toBe("file");
  expect(bash!.result?.payload.output).toBe("ok");
  expect(isShell(bash!)).toBe(true);
  expect(isShell(read!)).toBe(false);

  const noIDs = rows([ev("tool_call", { name: "Read" }), ev("tool_result", { content: "x" }), ev("assistant_message", { text: "hi" }),
    ev("tool_call", { name: "Bash" }), ev("command_output", { output: "y" })]);
  expect(noIDs.map((r) => r.kind)).toEqual(["tool", "event", "tool"]);

  // A result whose call is not in the transcript, and a call still running.
  const loose = rows([ev("command_output", { tool_call_id: "gone", output: "z" }), ev("tool_call", { id: "t9", name: "Grep" })]);
  expect(loose.map((r) => r.kind)).toEqual(["event", "tool"]);
  expect((loose[1] as { result?: RunEvent }).result).toBeUndefined();
});

test("follows the end of the transcript only while it is scrolled there", () => {
  expect(atBottom(600, 400, 1000)).toBe(true);
  expect(atBottom(590, 400, 1000)).toBe(true);
  expect(atBottom(300, 400, 1000)).toBe(false);
});

test("colors diff lines", () => {
  expect(["@@ -1 +1 @@", "+added", "-removed", " same", "+++ b/file", "--- a/file"].map(diffLine))
    .toEqual(["hunk", "add", "del", "context", "context", "context"]);
});

test("says whether the transcript is live", () => {
  expect(connection("running", { end: null, reconnecting: false })).toBe("live");
  expect(connection("running", { end: null, reconnecting: true })).toBe("reconnecting");
  expect(connection("running", { end: "succeeded", reconnecting: false })).toBe("finished");
  expect(connection("failed", { end: null, reconnecting: false })).toBe("finished");
  expect(connection(undefined, { end: null, reconnecting: false })).toBe("live");
});

test("crumbs after the issue or release name the run, then the step", () => {
  const id = "00000000-0000-7000-8000-000000002111";
  expect(runCrumbs(id, undefined)).toEqual([{ label: "Run 00002111" }]);
  expect(runCrumbs(id, { id: "s", name: "implement (attempt 2)" })).toEqual([{ label: "Run 00002111", href: `/runs/${id}` }, { label: "implement (attempt 2)" }]);
  expect(releaseRunCrumbs({ title: "demo", prefix: "vfy" }, { id: "r1", name: "v0.1.0" })).toEqual([
    { label: "demo", href: "/projects/vfy/board" }, { label: "Releases", href: "/projects/vfy/releases" }, { label: "Release v0.1.0", href: "/releases/r1" },
  ]);
  expect(releaseRunCrumbs(undefined, { id: "r1", name: "fedcba9" })).toEqual([{ label: "Release fedcba9", href: "/releases/r1" }]);
});
