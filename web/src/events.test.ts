import { expect, test } from "bun:test";
import { summary, type RunEvent } from "./events";

const ev = (type: string, payload: Record<string, unknown>): RunEvent => ({ seq: 1, time: "", type, payload });

test("summarizes each event type", () => {
  expect(summary(ev("assistant_message", { text: "hi" }))).toBe("hi");
  expect(summary(ev("tool_call", { name: "Bash", input: { command: "ls" } }))).toBe('Bash {"command":"ls"}');
  expect(summary(ev("token_usage", { cost_usd: 0.0832117, turns: 7, output_tokens: 1678 }))).toBe("$0.0832 · 7 turns · 1678 output tokens");
  expect(summary(ev("state_change", { entity: "run", from: "running", to: "succeeded" }))).toBe("run: running → succeeded");
  expect(summary(ev("done", { status: "succeeded", commit_sha: "abc" }))).toBe("succeeded · abc");
  expect(summary(ev("error", { message: "run failed", detail: "no changes" }))).toBe("run failed: no changes");
  expect(summary(ev("file_diff", { path: "a.go", diff: "+x" }))).toBe("a.go\n+x");
});

test("tolerates missing fields", () => {
  expect(summary(ev("assistant_message", {}))).toBe("");
  expect(summary(ev("unknown_kind", { a: 1 }))).toBe('{"a":1}');
});
