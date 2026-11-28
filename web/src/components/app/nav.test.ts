import { expect, test } from "bun:test";
import { activeNav, navItems } from "./nav";

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
