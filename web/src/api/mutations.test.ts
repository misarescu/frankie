import { expect, test } from "bun:test";
import { staleAfter } from "./mutations";

// What each write invalidates: what it changed must show without a reload.
test("starting, retrying, completing and canceling refresh the board, the issues and the runs", () => {
  for (const w of ["start", "retry", "complete", "cancel"] as const) {
    expect(staleAfter(w)).toContainEqual(["issues"]);
    expect(staleAfter(w)).toContainEqual(["issue"]);
    expect(staleAfter(w)).toContainEqual(["runs"]);
  }
});

test("a gate decision refreshes the inbox, the run and the release it held", () => {
  const k = staleAfter("decide-gate");
  for (const prefix of [["approvals"], ["run"], ["issue"], ["release"], ["releases"]]) expect(k).toContainEqual(prefix);
});

test("an answer refreshes the questions; a spec decision the specs and that issue", () => {
  expect(staleAfter("answer")).toContainEqual(["questions"]);
  const spec = staleAfter("decide-spec", "gc-88");
  expect(spec).toContainEqual(["specs"]);
  expect(spec).toContainEqual(["issue", "gc-88"]);
  expect(spec).toContainEqual(["issues"]);
});

test("steer and abort refresh the run", () => {
  expect(staleAfter("steer")).toContainEqual(["run"]);
  expect(staleAfter("abort")).toContainEqual(["run"]);
});
