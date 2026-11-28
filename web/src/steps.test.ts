import { expect, test } from "bun:test";
import { abort, runningAgentStep, shownStatus, steer, stepName, timeline, totals, usd, waitNote, who, type Step } from "./steps";

const step = (step_id: string, kind: string, status: string, cost_usd = 0, input_tokens = 0, output_tokens = 0): Step =>
  ({ id: `id-${step_id}`, step_id, attempt: 1, kind, status, cost_usd, input_tokens, output_tokens });

test("adds up a run's usage", () => {
  expect(totals([step("implement", "agent", "succeeded", 0.4, 1000, 50), step("checks", "gate", "succeeded"), step("review", "agent", "running", 0.15, 300, 20)]))
    .toEqual({ cost: 0.55, input: 1300, output: 70 });
  expect(usd(0)).toBe("$0.00");
  expect(usd(0.004)).toBe("<$0.01");
  expect(usd(1.234)).toBe("$1.23");
});

test("finds the running agent step", () => {
  expect(runningAgentStep([step("implement", "agent", "succeeded"), step("checks", "gate", "running")])).toBeUndefined();
  expect(runningAgentStep([step("implement", "agent", "succeeded"), step("review", "agent", "running")])?.step_id).toBe("review");
  expect(runningAgentStep([])).toBeUndefined();
});

test("steer and abort post JSON to the step", async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fetchFn = async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    return new Response(null, { status: 202 });
  };
  await steer(fetchFn, "s1", "  use the helper ");
  await abort(fetchFn, "s1", "wrong approach");
  expect(calls.map((c) => c.url)).toEqual(["/api/steps/s1/steer", "/api/steps/s1/abort"]);
  expect(JSON.parse(String(calls[0]!.init?.body))).toEqual({ text: "use the helper" });
  expect(JSON.parse(String(calls[1]!.init?.body))).toEqual({ reason: "wrong approach" });
  expect(new Headers(calls[0]!.init?.headers).get("Content-Type")).toBe("application/json");
});

test("explains a step that ended meanwhile, and other refusals", async () => {
  const answer = (status: number, body = "") => async () => new Response(body, { status });
  await expect(steer(answer(409), "s1", "x")).rejects.toThrow("no longer running");
  await expect(abort(answer(400, "reason is required\n"), "s1", "x")).rejects.toThrow("reason is required");
});

test("M5.1: says who does a step's work and names its attempt", () => {
  const base = step("implement", "agent", "running");
  expect(who({ ...base, role: "coder", harness: "claude-code", model: "anthropic/claude-sonnet-5-5" })).toBe("coder · claude-code · anthropic/claude-sonnet-5-5");
  expect(who({ ...base, role: "coder", harness: "pi" })).toBe("coder · pi · default model");
  expect(who({ ...step("checks", "gate", "running"), gate: "command" })).toBe("command gate");
  expect(who(step("open-change-request", "action", "succeeded"))).toBe("action");
  expect(who(step("old", "agent", "succeeded"))).toBe("");
  expect(stepName(base)).toBe("implement");
  expect(stepName({ ...base, attempt: 3 })).toBe("implement (attempt 3)");
});

test("M5.1: steer and abort send the person's name as by", async () => {
  const bodies: unknown[] = [];
  const fetchFn = async (_url: string, init?: RequestInit) => {
    bodies.push(JSON.parse(String(init?.body)));
    return new Response(null, { status: 202 });
  };
  await steer(fetchFn, "s1", "go", "Ana");
  await abort(fetchFn, "s1", "stop", "Ana");
  expect(bodies).toEqual([{ text: "go", by: "Ana" }, { reason: "stop", by: "Ana" }]);
});

test("M5.1: the steps not reached yet follow the ones that ran, in plan order", () => {
  const plan = [
    { step_id: "implement", kind: "agent", role: "coder" }, { step_id: "checks", kind: "gate", gate: "command" },
    { step_id: "code-reviewer", kind: "agent", role: "code-reviewer" }, { step_id: "review", kind: "gate", gate: "agent-verdict" },
  ];
  const ran = [step("implement", "agent", "succeeded"), step("checks", "gate", "running")];
  const got = timeline(plan, ran, false);
  expect(got.map((s) => [s.step_id, s.status, who(s)])).toEqual([
    ["implement", "succeeded", ""], ["checks", "running", ""],
    ["code-reviewer", "not started", "code-reviewer · default model"], ["review", "not started", "agent-verdict gate"],
  ]);
  // A run that ended will not reach them.
  expect(timeline(plan, ran, true)).toEqual(ran);
});

test("D98: a conditional step the run went past shows as skipped, with its condition, where it would have run", () => {
  const testable = (equals: boolean) => ({ step: "investigate", field: "testable", equals });
  const plan = [
    { step_id: "investigate", kind: "agent", role: "bug-investigator" },
    { step_id: "decide", kind: "gate", gate: "human", when: testable(false) },
    { step_id: "reproduce", kind: "agent", role: "bug-reproducer", when: testable(true) },
    { step_id: "fix", kind: "agent", role: "coder" },
    { step_id: "keeps-test", kind: "gate", gate: "preserve", when: testable(true) },
    { step_id: "checks", kind: "gate", gate: "command" },
  ];
  const shown = (ran: Step[], ended: boolean) => timeline(plan, ran, ended).map((s) => `${s.step_id}:${s.status}`);
  // Before the run goes past it, a conditional step is still to come.
  expect(shown([step("investigate", "agent", "running")], false))
    .toEqual(["investigate:running", "decide:not started", "reproduce:not started", "fix:not started", "keeps-test:not started", "checks:not started"]);
  // Testable: decide is skipped, in its place, while the run goes and after it ends.
  const testableRun = [step("investigate", "agent", "succeeded"), step("reproduce", "agent", "succeeded"), step("fix", "agent", "running")];
  expect(shown(testableRun, false))
    .toEqual(["investigate:succeeded", "decide:skipped", "reproduce:succeeded", "fix:running", "keeps-test:not started", "checks:not started"]);
  expect(timeline(plan, testableRun, false)[1]?.skipped_because).toBe("runs only when investigate's testable is false");
  // Not testable: the steps for a test are skipped; one the run never reached is not.
  const untestable = [step("investigate", "agent", "succeeded"), step("decide", "gate", "succeeded"), step("fix", "agent", "succeeded"), step("checks", "gate", "failed")];
  expect(shown(untestable, true))
    .toEqual(["investigate:succeeded", "decide:succeeded", "reproduce:skipped", "fix:succeeded", "keeps-test:skipped", "checks:failed"]);
  expect(shown(untestable.slice(0, 3), true)).toEqual(["investigate:succeeded", "decide:succeeded", "reproduce:skipped", "fix:succeeded"]);
  // A loop back runs earlier steps again: the skipped step keeps its place.
  const looped = [...untestable, { ...step("fix", "agent", "running"), id: "fix-2", attempt: 2 }];
  expect(shown(looped, false))
    .toEqual(["investigate:succeeded", "decide:succeeded", "reproduce:skipped", "fix:succeeded", "keeps-test:skipped", "checks:failed", "fix:running"]);
});

test("M5.1: a step waiting for a person shows as awaiting a human, with where to act", () => {
  const gate = { ...step("protected-changes", "gate", "running"), waits_for: "approval" as const };
  expect(shownStatus(gate)).toBe("awaiting_human");
  expect(waitNote(gate)).toEqual({ text: "decide it in Approvals", href: "/approvals?kind=gate" });
  const asking = { ...step("implement", "agent", "running"), waits_for: "answer" as const };
  expect(shownStatus(asking)).toBe("awaiting_human");
  expect(waitNote(asking)?.text).toBe("answer it in Approvals");
  expect(shownStatus(step("checks", "gate", "running"))).toBe("running");
  expect(waitNote(step("checks", "gate", "running"))).toBeUndefined();
  expect(shownStatus({ ...gate, status: "succeeded" })).toBe("succeeded");
});


test("M6: a step whose image is being built says so, with where its log is", () => {
  const building = { ...step("implement", "agent", "pending"), waits_for: "toolchain-image" as const };
  expect(shownStatus(building)).toBe("building_image");
  expect(waitNote(building, "/runs/r1")).toEqual({ text: "building the toolchain image: see its log", href: "/runs/r1" });
  expect(waitNote(building)).toEqual({ text: "building the toolchain image; its log is below" });
  const deps = { ...step("checks", "gate", "running"), waits_for: "dependency-image" as const };
  expect(waitNote(deps)?.text).toBe("building the dependency image; its log is below");
  expect(shownStatus({ ...building, status: "failed" })).toBe("failed");
});
