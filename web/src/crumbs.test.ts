import { expect, test } from "bun:test";
import { issueChain, pageTitle, projectCrumb, projectTitle } from "./crumbs";
import type { IssueSummary } from "./issues";

const issue = (key: string, title: string, parent?: string): IssueSummary => ({
  key, number: 1, type: "task", title, status: "ready", priority: 3, parent, depends_on: [], labels: [], cost_usd: 0, total_cost_usd: 0,
});
const project = { name: "misarescu/Goodreads-Clone", key_prefix: "gc" };
const issues = [issue("gc-80", "Reading lists"), issue("gc-81", "Shelves", "gc-80"), issue("gc-83", "Add books to a shelf", "gc-81")];

test("a project reads as its repository's name and links to its board", () => {
  expect(projectTitle(project)).toBe("Goodreads-Clone");
  expect(projectTitle({ name: "frankie" })).toBe("frankie");
  expect(projectCrumb(project)).toEqual({ label: "Goodreads-Clone", href: "/projects/gc/board" });
});

test("an issue's crumbs walk project, epic, story, task", () => {
  expect(issueChain(project, issues, "gc-83", { titles: true })).toEqual([
    { label: "Goodreads-Clone", href: "/projects/gc/board" },
    { label: "gc-80 Reading lists", href: "/issues/gc-80" },
    { label: "gc-81 Shelves", href: "/issues/gc-81" },
    { label: "gc-83 Add books to a shelf" },
  ]);
});

test("under a run the chain shows keys and links the issue", () => {
  expect(issueChain(project, issues, "gc-83", { titles: false, linkLast: true }).slice(1)).toEqual([
    { label: "gc-80", href: "/issues/gc-80" },
    { label: "gc-81", href: "/issues/gc-81" },
    { label: "gc-83", href: "/issues/gc-83" },
  ]);
});

test("an issue not in the list (yet) still has its crumb, with the title the page knows", () => {
  expect(issueChain(project, [], "gc-99", { titles: true, title: "New" })).toEqual([
    { label: "Goodreads-Clone", href: "/projects/gc/board" },
    { label: "gc-99 New" },
  ]);
  expect(issueChain(undefined, [], "gc-99", { titles: true })).toEqual([{ label: "gc-99" }]);
});

test("M9: the page title names the page, and what it sits in when that is ambiguous", () => {
  const p = { label: "demo", href: "/projects/vfy/board" };
  expect(pageTitle([])).toBe("Frankie");
  expect(pageTitle([p, { label: "vfy-8 Accounts" }])).toBe("vfy-8 Accounts · Frankie");
  expect(pageTitle([p, { label: "Board" }])).toBe("Board · demo · Frankie");
  expect(pageTitle([p, { label: "Approvals" }])).toBe("Approvals · Frankie");
  expect(pageTitle([p, { label: "vfy-8", href: "/issues/vfy-8" }, { label: "Run 00002113", href: "/runs/x" }, { label: "fix" }])).toBe("fix · Run 00002113 · Frankie");
  expect(pageTitle([p, { label: "vfy-8", href: "/issues/vfy-8" }, { label: "Run 00002113" }])).toBe("Run 00002113 · Frankie");
  expect(pageTitle([p, { label: "Releases", href: "/projects/vfy/releases" }, { label: "v0.1.0" }])).toBe("v0.1.0 · Releases · Frankie");
  expect(pageTitle([{ label: "Not found" }])).toBe("Not found · Frankie");
});
