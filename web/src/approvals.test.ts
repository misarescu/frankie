import { expect, test } from "bun:test";
import { approvalHelp, decide, expiresIn } from "./approvals";

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

test("D146: decide posts decision and trimmed comment without client-supplied by", async () => {
  const bodies: unknown[] = [];
  const fetchFn = async (_url: string, init?: RequestInit) => {
    bodies.push(JSON.parse(String(init?.body)));
    return new Response(null, { status: 202 });
  };
  await decide(fetchFn, "g1", "approve", " ok ");
  expect(bodies).toEqual([{ decision: "approve", comment: "ok" }]);
});

test("M6: a gate before a merge offers request changes and says what reject does", () => {
  expect(approvalHelp({}).reject).toBe("Reject");
  const merge = approvalHelp({ can_request_changes: true });
  expect(merge.reject).toBe("Reject and close");
  expect(merge.placeholder).toContain("required to request changes");
});

test("M6: request-changes is sent as such", async () => {
  const bodies: unknown[] = [];
  const fetchFn = async (_: string, init?: RequestInit) => {
    bodies.push(JSON.parse(String(init?.body)));
    return new Response(null, { status: 202 });
  };
  await decide(fetchFn, "g1", "request-changes", " keep both routes ");
  expect(bodies).toEqual([{ decision: "request-changes", comment: "keep both routes" }]);
});
