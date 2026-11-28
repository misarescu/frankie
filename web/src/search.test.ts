import { expect, test } from "bun:test";
import { approvalsSearch, boardSearch, issueSearch, runSearch, runsSearch } from "./search";

test("board search", () => {
  expect(boardSearch({})).toEqual({ view: undefined, finished: undefined });
  expect(boardSearch({ view: "graph", finished: 1 })).toEqual({ view: "graph", finished: 1 });
  expect(boardSearch({ view: "tree", finished: "1" })).toEqual({ view: undefined, finished: 1 });
  expect(boardSearch({ finished: 0 }).finished).toBeUndefined();
});

test("issue, runs and approvals search drop unknown values", () => {
  expect(issueSearch({ tab: "runs" }).tab).toBe("runs");
  expect(issueSearch({ tab: "children" }).tab).toBeUndefined();
  expect(runsSearch({ status: "failed" }).status).toBe("failed");
  expect(runsSearch({ status: "done" }).status).toBeUndefined();
  expect(approvalsSearch({ kind: "question" }).kind).toBe("question");
  expect(approvalsSearch({ kind: 3 }).kind).toBeUndefined();
});

test("run search keeps the selected step", () => {
  expect(runSearch({ step: "0198f2a4-2222-7000-8000-000000000002" }).step).toBe("0198f2a4-2222-7000-8000-000000000002");
  expect(runSearch({ step: 12 }).step).toBe("12"); // the router parses a numeric-looking value as a number
  expect(runSearch({ step: "" }).step).toBeUndefined();
  expect(runSearch({}).step).toBeUndefined();
});
