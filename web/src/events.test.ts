import { expect, test } from "bun:test";
import { isConnectionError, label, parseStreamEvent, segments, summary, type RunEvent } from "./events";

const ev = (type: string, payload: Record<string, unknown>): RunEvent => ({ seq: 1, time: "", type, payload });

test("summarizes each event type", () => {
  expect(summary(ev("assistant_message", { text: "hi" }))).toBe("hi");
  expect(summary(ev("tool_call", { name: "Bash", input: { command: "ls" } }))).toBe("ls");
  expect(summary(ev("tool_call", { name: "Read", input: { file_path: "a.go" } }))).toBe("a.go");
  expect(summary(ev("tool_call", { name: "Task", input: { prompt: "x" } }))).toBe('{"prompt":"x"}');
  expect(summary(ev("token_usage", { cost_usd: 0.0832117, turns: 7, output_tokens: 1678 }))).toBe("$0.0832 · 7 turns · 1678 output tokens");
  expect(summary(ev("state_change", { entity: "run", from: "running", to: "succeeded" }))).toBe("Run: running → succeeded");
  expect(summary(ev("state_change", { entity: "step_run", from: "pending", to: "running" }))).toBe("Step: pending → running");
  expect(summary(ev("done", { status: "succeeded", commit_sha: "abc" }))).toBe("succeeded · abc");
  expect(summary(ev("error", { message: "run failed", detail: "no changes" }))).toBe("run failed: no changes");
  expect(summary(ev("file_diff", { path: "a.go", diff: "+x" }))).toBe("a.go\n+x");
  expect(summary(ev("intervention", { action: "steer", by: "ana", text: "use the helper", delivered: true }))).toBe("ana steered: use the helper");
  expect(summary(ev("question", { question_id: "q1", question: "Which port?" }))).toBe("asked: Which port?");
  expect(summary(ev("intervention", { action: "abort", by: "ana", text: "wrong", delivered: false }))).toBe("ana aborted: wrong (the session had already ended)");
  expect(summary(ev("intervention", { action: "answer", by: "ana", text: "8080", question_id: "q1", delivered: true }))).toBe("ana answered: 8080");
  expect(summary(ev("intervention", { action: "answer", by: "ana", text: "8080", question_id: "q1", delivered: false }))).toBe("ana answered: 8080 (the agent no longer waited for it)");
});

test("tolerates missing fields", () => {
  expect(summary(ev("assistant_message", {}))).toBe("");
  expect(summary(ev("unknown_kind", { a: 1 }))).toBe('{"a":1}');
});

// EventSource fires its own "error" event (no data) when the connection drops,
// under the same name as the transcript's "error" events.
test("parses transcript messages and ignores EventSource's own events", () => {
  const msg = new MessageEvent("error", { data: JSON.stringify(ev("error", { message: "run failed" })) });
  expect(parseStreamEvent(msg)?.payload.message).toBe("run failed");
  expect(parseStreamEvent(new Event("error"))).toBeNull();
  expect(parseStreamEvent(new MessageEvent("tool_call", { data: "not json" }))).toBeNull();
});

test("tells a dropped connection from a transcript error event", () => {
  expect(isConnectionError(new Event("error"))).toBe(true);
  expect(isConnectionError(new MessageEvent("error", { data: "{}" }))).toBe(false);
});

test("M5.1: labels events in plain words", () => {
  expect(label(ev("assistant_message", {}))).toBe("Agent");
  expect(label(ev("tool_call", { name: "Bash" }))).toBe("Tool: Bash");
  expect(label(ev("state_change", {}))).toBe("Status");
  expect(label(ev("command_output", {}))).toBe("Command output");
  expect(label(ev("intervention", { action: "answer" }))).toBe("Answer");
  expect(label(ev("intervention", { action: "steer" }))).toBe("Steer");
  expect(label(ev("new_kind", {}))).toBe("new kind");
});

test("M5.1: summarizes gate results by command and by reviewer", () => {
  expect(summary(ev("gate_result", { type: "command", verdict: "fail",
    details: { commands: [{ command: "mise run lint", exitCode: 0 }, { command: "mise run test", exitCode: 1 }, { command: "x", exitCode: null, reason: "timeout" }] } })))
    .toBe("command gate failed\nmise run lint: exit 0\nmise run test: exit 1\nx: did not run (timeout)");
  expect(summary(ev("gate_result", { type: "agent-verdict", verdict: "pass",
    details: { reviews: [{ role: "code-reviewer", verdict: "pass", summary: "looks right" }, { step: "b", error: "no JSON" }] } })))
    .toBe("agent-verdict gate passed\ncode-reviewer pass: looks right\nb no verdict: no JSON");
  // M6 (D90): the repo's own CI, failed checks first.
  expect(summary(ev("gate_result", { type: "provider-checks", verdict: "fail", details: { sha: "abc", state: "failure",
    checks: [{ name: "lint", state: "success" }, { name: "test", state: "failure", summary: "2 tests failed", url: "https://ci/1" }] } })))
    .toBe("provider-checks gate failed\ntest: failure (2 tests failed) https://ci/1\nlint: success");
  // M7 (D120): a failed check's log follows it.
  expect(summary(ev("gate_result", { type: "provider-checks", verdict: "fail", details: { sha: "abc", state: "failure",
    checks: [{ name: "test", state: "failure", url: "https://ci/1", log: "Run make test\nError: exit 2" }] } })))
    .toBe("provider-checks gate failed\ntest: failure https://ci/1\nRun make test\nError: exit 2");
  expect(summary(ev("gate_result", { type: "provider-checks", verdict: "pass", details: { sha: "abc", state: "none" } })))
    .toBe("provider-checks gate passed\nthe repo has no CI checks on this commit");
  expect(summary(ev("gate_result", { type: "human", verdict: "escalated" }))).toBe("human gate escalated to a person");
});

test("M5.1: groups consecutive events by step execution", () => {
  const at = (seq: number, step?: string): RunEvent => ({ seq, time: "", type: "tool_call", payload: {}, ...(step ? { step_run_id: step } : {}) });
  const got = segments([at(1), at(2, "a"), at(3, "a"), at(4, "b"), at(5, "a"), at(6)]);
  expect(got.map((s) => [s.stepRunID ?? "run", s.events.map((e) => e.seq)])).toEqual([
    ["run", [1]], ["a", [2, 3]], ["b", [4]], ["a", [5]], ["run", [6]],
  ]);
  expect(segments([])).toEqual([]);
});

test("M6: image builds and their logs read as such", () => {
  expect(label(ev("image_build", {}))).toBe("Image build");
  expect(summary(ev("image_build", { image: "toolchain", tag: "tc-1", status: "building" }))).toBe("building the toolchain image tc-1");
  expect(summary(ev("image_build", { image: "dependencies", status: "built", seconds: 61.4 }))).toBe("built the dependency image in 61s");
  expect(summary(ev("image_build", { image: "toolchain", status: "failed", seconds: 39, error: "nodejs.org refused" })))
    .toBe("the toolchain image build failed in 39s: nodejs.org refused");
  expect(summary(ev("build_log", { output: "mise install node@22\n" }))).toBe("mise install node@22\n");
});

test("M6: an investigation's findings read as such", () => {
  expect(label(ev("findings", {}))).toBe("Findings");
  expect(summary(ev("findings", { findings: { root_cause: "stats sum shelf rows", confidence: "high", testable: true } })))
    .toBe("root cause (high confidence): stats sum shelf rows\na test can show it");
  expect(summary(ev("findings", { error: "the final message holds no JSON findings" }))).toBe("no valid findings: the final message holds no JSON findings");
});

test("M7: a preview gate says where it checked the head and what failed", () => {
  expect(summary(ev("gate_result", { type: "preview", verdict: "fail", details: { changeRequest: 152, sha: "ddddddd1234", url: "http://p/pr-152",
    ran: true, summary: "2 of 219 tests failed", failures: [{ test: "gc-9 list tags", message: "expected 200, got 500" }, { test: "gc-9 prefix" }], removed: true } })))
    .toBe("preview gate failed\n#152 at ddddddd on http://p/pr-152\n2 of 219 tests failed\ngc-9 list tags: expected 200, got 500\ngc-9 prefix");
  expect(summary(ev("gate_result", { type: "preview", verdict: "fail", details: { changeRequest: 152, sha: "ddddddd", url: "http://p/pr-152",
    summary: "no room for a preview", inconclusive: true, removeError: "github: 502" } })))
    .toBe("preview gate failed\n#152 at ddddddd on http://p/pr-152\nno room for a preview\nnot removed: github: 502");
});

test("M6: gates for bugs say what they expect and keep", () => {
  expect(summary(ev("gate_result", { type: "command", verdict: "pass", details: { expect_fail: true, commands: [{ command: "mise run test", exitCode: 1 }] } })))
    .toBe("command gate passed\nexpects the commands to fail (a test that shows the bug)\nmise run test: exit 1");
  expect(summary(ev("gate_result", { type: "preserve", verdict: "fail", details: { step: "reproduce", files: ["a_test.go"], changed: [{ file: "a_test.go", status: "modified" }] } })))
    .toBe("preserve gate failed\na_test.go: modified after reproduce wrote it");
  // D97: the lines the step added that the fix lost.
  expect(summary(ev("gate_result", { type: "preserve", verdict: "fail", details: { step: "reproduce", files: ["a_test.go"],
    changed: [{ file: "a_test.go", status: "modified", lines: ["check(t, n, 3)"] }] } })))
    .toBe("preserve gate failed\na_test.go: modified after reproduce wrote it\n  no longer there: check(t, n, 3)");
  expect(summary(ev("gate_result", { type: "preserve", verdict: "pass", details: { step: "reproduce", files: ["a_test.go"] } })))
    .toBe("preserve gate passed\nkeeps what reproduce wrote in 1 files");
});
