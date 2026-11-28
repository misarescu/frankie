import { expect, test } from "bun:test";
import { ApiError, getJSON, issueQuery, keys } from "./queries";

const answer = (status: number, body: string) => (async () => new Response(body, { status })) as unknown as typeof fetch;

test("getJSON returns what the API answered", async () => {
  expect(await getJSON("/api/runs", answer(200, '[{"ID":"r1"}]'))).toEqual([{ ID: "r1" }]);
});

test("getJSON says the control plane is down when the web server answers 502", async () => {
  const e = await getJSON("/api/runs", answer(502, "bad gateway")).catch((e: ApiError) => e);
  expect(e).toBeInstanceOf(ApiError);
  expect((e as ApiError).status).toBe(502);
  expect((e as ApiError).message).toBe("the control plane is not reachable");
});

test("getJSON carries the API's message, or the status without one", async () => {
  expect((await getJSON("/api/issues/x", answer(404, "no such issue\n")).catch((e: Error) => e) as Error).message).toBe("no such issue");
  expect((await getJSON("/api/issues/x", answer(500, "")).catch((e: Error) => e) as Error).message).toBe("status 500");
});

test("getJSON says when the web server itself did not answer", async () => {
  const down = (async () => { throw new TypeError("Failed to fetch"); }) as unknown as typeof fetch;
  const e = (await getJSON("/api/runs", down).catch((e: ApiError) => e)) as ApiError;
  expect(e.status).toBe(0);
  expect(e.message).toContain("did not answer");
});

// D141: the query keys are shared by every page; they are part of the contract.
test("query keys", () => {
  expect(keys.issues("verify/demo")).toEqual(["issues", "verify/demo"]);
  expect(keys.runEvents("r1")).toEqual(["run", "r1", "events"]);
  expect(issueQuery("gc-1").queryKey).toEqual(["issue", "gc-1"]);
});
