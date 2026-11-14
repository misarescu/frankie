import { expect, test } from "bun:test";
import { answer } from "./questions";

test("answer posts JSON to the question's answer endpoint", async () => {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fetchFn = async (url: string, init?: RequestInit) => {
    calls.push({ url, init });
    return new Response(null, { status: 202 });
  };
  await answer(fetchFn, "0198f2a4-1111-7000-8000-000000000001", "  port 8080  ");
  expect(calls).toHaveLength(1);
  expect(calls[0]!.url).toBe("/api/questions/0198f2a4-1111-7000-8000-000000000001/answer");
  expect(calls[0]!.init?.method).toBe("POST");
  expect(new Headers(calls[0]!.init?.headers).get("Content-Type")).toBe("application/json");
  expect(JSON.parse(String(calls[0]!.init?.body))).toEqual({ text: "port 8080" });
});

test("answer reports a question that no longer waits and other failures", async () => {
  const reply = (status: number, body = "") => async () => new Response(body, { status });
  await expect(answer(reply(409, "the question was already answered\n"), "q", "x")).rejects.toThrow("already answered");
  await expect(answer(reply(409), "q", "x")).rejects.toThrow("no longer waits");
  await expect(answer(reply(502, "could not reach the run's workflow\n"), "q", "x")).rejects.toThrow("could not reach");
});
