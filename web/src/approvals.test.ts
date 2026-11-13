import { expect, test } from "bun:test";
import { decide, expiresIn } from "./approvals";

test("decide posts JSON to the gate's decision endpoint", async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fetchFn = async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    return new Response(null, { status: 202 });
  };
  await decide(fetchFn, "0198f2a4-1111-7000-8000-000000000001", "reject", "  touches RBAC  ");
  expect(calls).toHaveLength(1);
  expect(calls[0]!.url).toBe("/api/gates/0198f2a4-1111-7000-8000-000000000001/decision");
  expect(calls[0]!.init?.method).toBe("POST");
  expect(new Headers(calls[0]!.init?.headers).get("Content-Type")).toBe("application/json");
  expect(JSON.parse(String(calls[0]!.init?.body))).toEqual({ decision: "reject", comment: "touches RBAC" });
});

test("decide reports a gate someone else decided and other failures", async () => {
  const answer = (status: number, body = "") => async () => new Response(body, { status });
  await expect(decide(answer(409), "g", "approve", "")).rejects.toThrow("already decided");
  await expect(decide(answer(502, "could not reach the run's workflow\n"), "g", "approve", "")).rejects.toThrow("could not reach");
});

test("expiresIn", () => {
  const now = new Date("2026-11-13T12:00:00Z");
  expect(expiresIn("2026-11-13T12:30:00Z", now)).toBe("30 min");
  expect(expiresIn("2026-11-14T12:00:00Z", now)).toBe("24 h");
  expect(expiresIn("2026-11-16T12:00:00Z", now)).toBe("3 days");
  expect(expiresIn("2026-11-13T11:00:00Z", now)).toBe("expired");
});
