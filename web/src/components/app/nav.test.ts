import { expect, test } from "bun:test";
import { activeNav, entityProject, navItems, pageEntity, switchProjectHref } from "./nav";

test("the sidebar marks the section a page belongs to", () => {
  expect(activeNav("/projects/gc/board")).toBe("board");
  expect(activeNav("/issues/gc-83")).toBe("board");
  expect(activeNav("/projects/gc/runs")).toBe("runs");
  expect(activeNav("/runs/r1")).toBe("runs");
  expect(activeNav("/projects/gc/releases")).toBe("releases");
  expect(activeNav("/releases/x")).toBe("releases");
  expect(activeNav("/approvals")).toBe("approvals");
  expect(activeNav("/no/such")).toBeUndefined();
});

test("project pages link to the current project, or home without one", () => {
  expect(navItems.map((i) => i.href("gc"))).toEqual(["/projects/gc/board", "/projects/gc/runs", "/approvals", "/projects/gc/releases"]);
  expect(navItems.map((i) => i.href(undefined))).toEqual(["/", "/", "/approvals", "/"]);
});

test("switchProjectHref keeps the page, its view and filter, and opens the board elsewhere", () => {
  expect(switchProjectHref("/projects/gc/runs?status=failed", "frk")).toBe("/projects/frk/runs?status=failed");
  expect(switchProjectHref("/projects/gc/board?view=graph&finished=1", "frk")).toBe("/projects/frk/board?view=graph&finished=1");
  expect(switchProjectHref("/projects/gc/releases", "frk")).toBe("/projects/frk/releases");
  expect(switchProjectHref("/projects/gc", "frk")).toBe("/projects/frk/board");
  expect(switchProjectHref("/issues/gc-12", "frk")).toBe("/projects/frk/board");
  expect(switchProjectHref("/approvals?kind=gate", "frk")).toBe("/projects/frk/board");
});

test("M9: a page belongs to the project of what it shows", () => {
  expect(pageEntity("/projects/gc/board")).toEqual({ kind: "project", prefix: "gc" });
  expect(pageEntity("/issues/frk-12")).toEqual({ kind: "issue", prefix: "frk" });
  expect(pageEntity("/runs/r1")).toEqual({ kind: "run", id: "r1" });
  expect(pageEntity("/releases/x?y=1")).toEqual({ kind: "release", id: "x" });
  expect(pageEntity("/approvals")).toBeUndefined();
  expect(pageEntity("/")).toBeUndefined();
});

test("M9: the shell shows a run's or a release's project once it loads", () => {
  const projects = [{ name: "misarescu/Goodreads-Clone", key_prefix: "gc" }, { name: "misarescu/Frankie", key_prefix: "frk" }];
  expect(entityProject(pageEntity("/issues/gc-83"), projects)).toBe("gc");
  expect(entityProject(pageEntity("/issues/zz-1"), projects)).toBeUndefined();
  expect(entityProject(pageEntity("/projects/frk/runs"), projects)).toBe("frk");
  const run = pageEntity("/runs/r1");
  expect(entityProject(run, projects)).toBeUndefined();
  expect(entityProject(run, projects, { run: { IssueKey: "gc-83" } })).toBe("gc");
  expect(entityProject(run, projects, { run: { IssueKey: "", ReleaseID: "rel" } })).toBeUndefined();
  expect(entityProject(run, projects, { run: { IssueKey: "", ReleaseID: "rel" }, release: { project: "misarescu/Goodreads-Clone" } })).toBe("gc");
  expect(entityProject(pageEntity("/releases/rel"), projects, { release: { project: "misarescu/Frankie" } })).toBe("frk");
  expect(entityProject(pageEntity("/approvals"), projects)).toBeUndefined();
});
