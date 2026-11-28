// Releases (D114, D129, D130): what GET /api/releases and
// GET /api/releases/{id} return, and how the releases page names them.

export type StageStatus = { stage: string; status: string };

export type ReleaseSummary = {
  id: string;
  repo: string;
  sha: string;
  status: string;
  reason?: string;
  version?: string;
  bump?: string;
  issues: string[];
  stages: StageStatus[];
  created_at: string;
  finished_at?: string;
};

export type TestFailure = { test: string; issueKeys?: string[]; message?: string };

export type Verification = {
  passed: boolean;
  inconclusive?: boolean;
  summary: string;
  failures?: TestFailure[];
};

export type Deployment = {
  stage: string;
  driver: string;
  status: string;
  reason?: string;
  verification?: Verification;
  started_at?: string;
  finished_at?: string;
};

export type ReleaseIssue = { key: string; type: string; title: string; status: string };

export type ReleaseRun = { id: string; status: string; steps: string[]; cost_usd: number; created_at: string };

export type ReleaseDetail = ReleaseSummary & {
  project: string;
  notes?: string;
  notes_source?: string;
  provider_release_url?: string;
  publish_error?: string;
  bug_issue_ref?: string;
  issue_details: ReleaseIssue[];
  deployments: Deployment[];
  runs: ReleaseRun[];
  cost_usd: number;
};

// shortSHA is a commit as people read it.
export const shortSHA = (sha: string): string => sha.slice(0, 7);

// releaseName is the release's version once it has one, else its commit.
export const releaseName = (r: { version?: string; sha: string }): string => r.version || shortSHA(r.sha);

// verificationText says how a stage's verification ended, in a line; empty
// when the deployment has none (not verified yet, or before D130).
export function verificationText(v: Verification | undefined): string {
  if (!v) return "";
  if (v.passed) return v.summary;
  if (v.inconclusive) return `could not judge the release: ${v.summary}`;
  return v.summary || "failed";
}

// verificationState is the chip a verification shows.
export function verificationState(v: Verification): "succeeded" | "failed" | "inconclusive" {
  if (v.passed) return "succeeded";
  return v.inconclusive ? "inconclusive" : "failed";
}

// notesSourceText says who wrote a release's notes (D129).
export function notesSourceText(source: string | undefined): string {
  switch (source) {
    case "agent":
      return "written by the release agent";
    case "fallback":
      return "the release agent's notes were not available; listed from the issues";
    default:
      return "";
  }
}

// versionText says what became of a release's version (D129): the version,
// why publishing it failed, or why it has none.
export function versionText(r: Pick<ReleaseDetail, "status" | "version" | "provider_release_url" | "publish_error">): string {
  if (r.version) return r.publish_error ? `${r.version}, not published: ${r.publish_error}` : r.version;
  switch (r.status) {
    case "pending":
    case "in_progress":
      return "versioned once every stage passes";
    case "succeeded":
      return r.publish_error ? `not versioned: ${r.publish_error}` : "not versioned";
    default:
      return "no version: its issues are in the next release";
  }
}

// isLive reports whether a release still moves through its stages.
export const isLive = (status: string): boolean => status === "pending" || status === "in_progress";

// pipelineStages is a release's stages as its pipeline shows them: the
// stages its deployments reached, and a stage whose human gate waits (D139),
// which has no deployment until someone approves, as waiting for a person.
export function pipelineStages(stages: StageStatus[], waiting: string[]): StageStatus[] {
  const out = stages.map((s) => (waiting.includes(s.stage) ? { ...s, status: "awaiting_human" } : s));
  for (const w of waiting) if (!out.some((s) => s.stage === w)) out.push({ stage: w, status: "awaiting_human" });
  return out;
}

// formatWhen is a time as the releases pages show it: date and time, short.
export const formatWhen = (iso: string, locale?: string, timeZone?: string): string =>
  new Date(iso).toLocaleString(locale, { dateStyle: "medium", timeStyle: "short", timeZone });
