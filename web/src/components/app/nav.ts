import { ActivityIcon, InboxIcon, RocketIcon, SquareKanbanIcon, type LucideIcon } from "lucide-react";
import { paths } from "@/paths";

export type NavID = "board" | "runs" | "approvals" | "releases";

// The sidebar's pages, with the icons the palette shows them with too.
export const navItems: { id: NavID; label: string; icon: LucideIcon; href: (project: string | undefined) => string }[] = [
  { id: "board", label: "Board", icon: SquareKanbanIcon, href: (p) => (p ? paths.board(p) : "/") },
  { id: "runs", label: "Runs", icon: ActivityIcon, href: (p) => (p ? paths.runs(p) : "/") },
  { id: "approvals", label: "Approvals", icon: InboxIcon, href: () => paths.approvals() },
  { id: "releases", label: "Releases", icon: RocketIcon, href: (p) => (p ? paths.releases(p) : "/") },
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
  return undefined;
}
