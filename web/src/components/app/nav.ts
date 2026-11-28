import { ActivityIcon, InboxIcon, KeyRoundIcon, RocketIcon, SquareKanbanIcon, UsersIcon, type LucideIcon } from "lucide-react";
import type { RunSummary } from "@/events";
import { keyPrefix, type Project } from "@/issues";
import { paths } from "@/paths";

export type NavID = "board" | "runs" | "approvals" | "releases" | "account" | "users";

// The sidebar's main pages, with the icons the palette shows them with too.
export const navItems: { id: NavID; label: string; icon: LucideIcon; href: (project: string | undefined) => string }[] = [
  { id: "board", label: "Board", icon: SquareKanbanIcon, href: (p) => (p ? paths.board(p) : "/") },
  { id: "runs", label: "Runs", icon: ActivityIcon, href: (p) => (p ? paths.runs(p) : "/") },
  { id: "approvals", label: "Approvals", icon: InboxIcon, href: () => paths.approvals() },
  { id: "releases", label: "Releases", icon: RocketIcon, href: (p) => (p ? paths.releases(p) : "/") },
];

export const settingsNavItems: { id: "account" | "users"; label: string; icon: LucideIcon; href: string; adminOnly?: boolean }[] = [
  { id: "account", label: "Account", icon: KeyRoundIcon, href: paths.account() },
  { id: "users", label: "Users", icon: UsersIcon, href: paths.users(), adminOnly: true },
];

// switchProjectHref is where picking another project goes: the same page
// of that project, with its view and filter, when the page is a project's
// (board, runs, releases); else that project's board.
export function switchProjectHref(href: string, project: string): string {
  const m = /^\/projects\/[^/?#]+(\/[^?#]*)?([?#].*)?$/.exec(href);
  if (!m?.[1] || m[1] === "/") return paths.board(project);
  return `/projects/${encodeURIComponent(project)}${m[1]}${m[2] ?? ""}`;
}

// activeNav is the sidebar entry a path belongs to: an issue is on the
// board, a run with the runs, a release with the releases.
export function activeNav(path: string): NavID | undefined {
  if (/^\/projects\/[^/]+\/board/.test(path) || path.startsWith("/issues/")) return "board";
  if (/^\/projects\/[^/]+\/runs/.test(path) || path.startsWith("/runs/")) return "runs";
  if (/^\/projects\/[^/]+\/releases/.test(path) || path.startsWith("/releases/")) return "releases";
  if (path.startsWith("/approvals")) return "approvals";
  if (path.startsWith("/settings/account")) return "account";
  if (path.startsWith("/settings/users")) return "users";
  return undefined;
}

// PageEntity is what a path shows that belongs to one project: a project's
// page, an issue (by its key's prefix), a run or a release (whose project
// is known once they load).
export type PageEntity =
  | { kind: "project" | "issue"; prefix: string }
  | { kind: "run" | "release"; id: string };

export function pageEntity(path: string): PageEntity | undefined {
  const m = /^\/(projects|issues|runs|releases)\/([^/?#]+)/.exec(path);
  if (!m?.[2]) return undefined;
  const v = decodeURIComponent(m[2]);
  switch (m[1]) {
    case "projects": return { kind: "project", prefix: v };
    case "issues": return { kind: "issue", prefix: keyPrefix(v) };
    case "runs": return { kind: "run", id: v };
    default: return { kind: "release", id: v };
  }
}

// entityProject is the key prefix of the project a page's entity belongs
// to: a run's is its issue's, or its release's; a release's is the project
// it names. Undefined while what it takes has not loaded, or for a project
// Frankie does not have.
export function entityProject(e: PageEntity | undefined, projects: Pick<Project, "name" | "key_prefix">[],
  loaded: { run?: Pick<RunSummary, "IssueKey" | "ReleaseID">; release?: { project: string } } = {}): string | undefined {
  const byPrefix = (p: string) => projects.find((x) => x.key_prefix === p)?.key_prefix;
  const byName = (n: string | undefined) => projects.find((x) => x.name === n)?.key_prefix;
  if (!e) return undefined;
  if (e.kind === "project" || e.kind === "issue") return byPrefix(e.prefix);
  if (e.kind === "run" && loaded.run?.IssueKey) return byPrefix(keyPrefix(loaded.run.IssueKey));
  return byName(loaded.release?.project);
}
