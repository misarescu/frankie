import { expect, test } from "bun:test";
import { route } from "./routes";

test("routes", () => {
  expect(route("")).toEqual({ page: "runs" });
  expect(route("#/")).toEqual({ page: "runs" });
  expect(route("#/runs/0198f2a4-2222-7000-8000-000000000002")).toEqual({ page: "run", id: "0198f2a4-2222-7000-8000-000000000002" });
  expect(route("#/approvals")).toEqual({ page: "approvals" });
  expect(route("#/issues")).toEqual({ page: "issues" });
  expect(route("#/issues/gc-12")).toEqual({ page: "issue", key: "gc-12" });
  expect(route("#/issues/gc-0")).toEqual({ page: "runs" });
  expect(route("#/issues/../x")).toEqual({ page: "runs" });
  // M7, D130.
  expect(route("#/releases")).toEqual({ page: "releases" });
  expect(route("#/releases/0198f2a4-2222-7000-8000-000000000003")).toEqual({ page: "release", id: "0198f2a4-2222-7000-8000-000000000003" });
  expect(route("#/releases/v0.1.0")).toEqual({ page: "runs" });
});
