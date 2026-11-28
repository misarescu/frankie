// The hash URLs of the UI before M9 (#/issues/gc-12) redirect to their path
// URLs (D141), so bookmarks and links in old messages keep working.

import { paths } from "./paths";

// legacyPath maps a pre-M9 hash route to its path, or null for a hash that is
// not one. Pages of one project (the board, the run and release lists) open
// in project, the one the UI would open anyway.
export function legacyPath(hash: string, project: string): string | null {
  const h = hash.startsWith("#") ? hash : `#${hash}`;
  const run = /^#\/runs\/([0-9a-f-]{36})$/.exec(h);
  if (run?.[1]) return paths.run(run[1]);
  const issue = /^#\/issues\/([a-z][a-z0-9]{1,9}-[1-9][0-9]*)$/.exec(h);
  if (issue?.[1]) return paths.issue(issue[1]);
  const release = /^#\/releases\/([0-9a-f-]{36})$/.exec(h);
  if (release?.[1]) return paths.release(release[1]);
  switch (h) {
    case "#/releases": return paths.releases(project);
    case "#/approvals": return paths.approvals();
    case "#/issues": return paths.board(project);
    case "#/":
    case "#/runs": return paths.runs(project);
  }
  return null;
}
