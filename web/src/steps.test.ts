import { expect, test } from "bun:test";
import { abort, runningAgentStep, steer, totals, usd, type Step } from "./steps";

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
