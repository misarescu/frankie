import { expect, test } from "bun:test";
import { decideSpec } from "./specs";

test("decideSpec posts the decision to the issue's spec endpoint", async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fetchFn = async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    return Response.json({ key: "gc-7", status: "ready" });
  };
  await decideSpec(fetchFn, "gc-7", "approve", "  looks right ");
  expect(calls[0]!.url).toBe("/api/issues/gc-7/spec");
  expect(calls[0]!.init?.method).toBe("POST");
  expect(JSON.parse(String(calls[0]!.init?.body))).toEqual({ decision: "approve", comment: "looks right" });
});

test("a rejection needs a comment, checked before anything is sent", async () => {
  let sent = 0;
  const fetchFn = async () => { sent++; return new Response(null, { status: 200 }); };
  await expect(decideSpec(fetchFn, "gc-7", "reject", "  ")).rejects.toThrow("what to change");
  expect(sent).toBe(0);
});

test("decideSpec reports the server's reason", async () => {
  const conflict = async () => new Response("gc-7 is ready; only a draft's spec can be decided\n", { status: 409 });
  await expect(decideSpec(conflict, "gc-7", "approve", "")).rejects.toThrow("only a draft");
});

test("M5.1: decideSpec sends the person's name as by", async () => {
  const bodies: unknown[] = [];
  const fetchFn = async (_url: string, init?: RequestInit) => {
    bodies.push(JSON.parse(String(init?.body)));
    return new Response(null, { status: 200 });
  };
  await decideSpec(fetchFn, "gc-7", "approve", "", "Ana");
  expect(bodies).toEqual([{ decision: "approve", comment: "", by: "Ana" }]);
});
