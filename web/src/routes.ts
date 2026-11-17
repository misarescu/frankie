// Hash routes keep the server trivial: #/runs/<id>, #/approvals, #/issues,
// #/issues/<key>, #/releases, #/releases/<id>, or the run list.
export type Route =
  | { page: "runs" }
  | { page: "run"; id: string }
  | { page: "approvals" }
  | { page: "issues" }
  | { page: "issue"; key: string }
  | { page: "releases" }
  | { page: "release"; id: string };

export function route(hash: string): Route {
  const run = /^#\/runs\/([0-9a-f-]{36})$/.exec(hash);
  if (run?.[1]) return { page: "run", id: run[1] };
  const issue = /^#\/issues\/([a-z][a-z0-9]{1,9}-[1-9][0-9]*)$/.exec(hash);
  if (issue?.[1]) return { page: "issue", key: issue[1] };
  const release = /^#\/releases\/([0-9a-f-]{36})$/.exec(hash);
  if (release?.[1]) return { page: "release", id: release[1] };
  if (hash === "#/releases") return { page: "releases" };
  if (hash === "#/approvals") return { page: "approvals" };
  if (hash === "#/issues") return { page: "issues" };
  return { page: "runs" };
}
