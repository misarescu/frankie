import { expect, test } from "bun:test";
import { activeNav, navItems, switchProjectHref } from "./nav";

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
