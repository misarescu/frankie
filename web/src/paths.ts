// The app's path URLs (D141). Pages, links, breadcrumbs and the domain
// modules' hrefs build them here, so a URL is spelled in one place; the
// router (router.tsx) declares the same paths with typed search params.

export type IssueTab = "overview" | "runs" | "issues" | "activity";
export type BoardView = "list" | "graph";
export type RunsFilter = "active" | "failed";
export type ApprovalKind = "gate" | "question" | "spec";

const enc = encodeURIComponent;

const query = (params: Record<string, string | number | undefined>): string => {
  const q = Object.entries(params).filter(([, v]) => v !== undefined && v !== "");
  return q.length ? `?${q.map(([k, v]) => `${k}=${enc(String(v))}`).join("&")}` : "";
};

export const paths = {
  board: (project: string, s: { view?: BoardView; finished?: 1 } = {}) =>
    `/projects/${enc(project)}/board${query({ view: s.view === "graph" ? "graph" : undefined, finished: s.finished })}`,
  runs: (project: string, status?: RunsFilter) => `/projects/${enc(project)}/runs${query({ status })}`,
  releases: (project: string) => `/projects/${enc(project)}/releases`,
  issue: (key: string, tab?: IssueTab) => `/issues/${enc(key)}${query({ tab: tab === "overview" ? undefined : tab })}`,
  run: (id: string, step?: string) => `/runs/${enc(id)}${query({ step })}`,
  release: (id: string) => `/releases/${enc(id)}`,
  approvals: (kind?: ApprovalKind) => `/approvals${query({ kind })}`,
};
