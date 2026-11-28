// Breadcrumbs (D141): where a page sits, project > epic > story > task >
// run > step, each part a link but the last.

import { ancestors, type IssueSummary, type Project } from "./issues";
import { paths } from "./paths";

export type Crumb = { label: string; href?: string };

// projectTitle is how the UI names a project: its repository's name,
// without the owner.
export const projectTitle = (p: Pick<Project, "name">): string => p.name.split("/").at(-1) || p.name;

export const projectCrumb = (p: Pick<Project, "name" | "key_prefix">): Crumb => ({ label: projectTitle(p), href: paths.board(p.key_prefix) });

// issueChain is the crumbs down to an issue: its project, the issues above
// it, then the issue. With titles, each issue reads "gc-81 Shelves"; without
// (under a run, where the chain is long), only its key. The issue itself is
// a link when more crumbs follow it.
export function issueChain(project: Pick<Project, "name" | "key_prefix"> | undefined, issues: IssueSummary[], key: string,
  opts: { titles: boolean; title?: string; linkLast?: boolean }): Crumb[] {
  const self = issues.find((i) => i.key === key);
  const name = (k: string, t?: string) => (opts.titles && t ? `${k} ${t}` : k);
  return [
    ...(project ? [projectCrumb(project)] : []),
    ...ancestors(issues, key).map((a) => ({ label: name(a.key, a.title), href: paths.issue(a.key) })),
    { label: name(key, self?.title ?? opts.title), ...(opts.linkLast ? { href: paths.issue(key) } : {}) },
  ];
}

// The pages a project has, whose crumb alone does not say which project.
const projectPages = new Set(["Board", "Runs", "Releases"]);

// pageTitle names the page in the browser's tab and history: its last
// crumb, with what it sits in when that alone is ambiguous (the run of a
// step, the releases of a release, the project of its board).
export function pageTitle(crumbs: Crumb[]): string {
  const last = crumbs.at(-1)?.label;
  if (!last) return "Frankie";
  const prev = crumbs.at(-2)?.label;
  const within = prev && (prev.startsWith("Run ") || prev === "Releases" || projectPages.has(last)) ? [prev] : [];
  return [last, ...within, "Frankie"].join(" · ");
}
