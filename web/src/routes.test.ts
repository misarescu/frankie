import { expect, test } from "bun:test";
import { legacyPath } from "./routes";

// M9, D141: every hash route of the old UI lands on its path.
test("legacy hash routes redirect to their paths", () => {
  expect(legacyPath("#/runs/0198f2a4-2222-7000-8000-000000000002", "gc")).toBe("/runs/0198f2a4-2222-7000-8000-000000000002");
  expect(legacyPath("#/issues/gc-12", "frk")).toBe("/issues/gc-12");
  expect(legacyPath("#/releases/0198f2a4-2222-7000-8000-000000000003", "gc")).toBe("/releases/0198f2a4-2222-7000-8000-000000000003");
  expect(legacyPath("#/issues", "gc")).toBe("/projects/gc/board");
  expect(legacyPath("#/releases", "gc")).toBe("/projects/gc/releases");
  expect(legacyPath("#/approvals", "gc")).toBe("/approvals");
  expect(legacyPath("#/", "gc")).toBe("/projects/gc/runs");
  // The router hands over the hash without its #.
  expect(legacyPath("/issues/gc-12", "gc")).toBe("/issues/gc-12");
});

test("anything else is not a legacy route", () => {
  for (const h of ["", "#", "#/issues/gc-0", "#/issues/../x", "#/releases/v0.1.0", "#/runs/abc", "#top"]) {
    expect(legacyPath(h, "gc")).toBeNull();
  }
});
