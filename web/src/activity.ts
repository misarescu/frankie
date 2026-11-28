// An issue's activity (M9): what happened to it, oldest first, derived only
// from the issue's own fields: its sync, start requests, spec decisions, each
// run starting and ending, and its closing (D70). Fields without a time (a
// spec sent back earlier, a change request) are shown elsewhere on the page.

import { shortID } from "./format";
import type { IssueDetail } from "./issues";

// sourceLabel names an issue's source briefly: "owner/repo#13" for a
// provider issue URL (.../owner/repo/issues/13), else the address itself.
export function sourceLabel(ref: string): string {
  const m = /^https?:\/\/[^/]+\/(.+?)\/(?:-\/)?issues\/(\d+)\/?$/.exec(ref);
  return m ? `${m[1]}#${m[2]}` : ref.replace(/^https?:\/\//, "");
}

export type ActivityEntry = {
  at: string;
  who?: string;
  what: string;
  // The run an entry is about, for its link.
  run?: string;
  tone?: "ok" | "bad" | "warn";
};

type Source = Pick<IssueDetail, "created_at" | "created_by" | "source_ref" | "runs"> &
  Partial<Pick<IssueDetail, "start_requested_at" | "start_requested_by" | "start_requested_via" | "spec_approved_at" | "spec_approved_by" |
    "spec_rejected_at" | "spec_feedback" | "closed_at" | "closed_by" | "close_reason" | "status">>;

const ended: Record<string, { what: string; tone: ActivityEntry["tone"] }> = {
  succeeded: { what: "succeeded", tone: "ok" },
  failed: { what: "failed", tone: "bad" },
  canceled: { what: "was canceled", tone: undefined },
  aborted: { what: "was aborted", tone: "bad" },
};

export function activity(is: Source): ActivityEntry[] {
  const out: ActivityEntry[] = [];
  out.push({ at: is.created_at, who: is.created_by || undefined, what: is.source_ref ? `synced it from ${sourceLabel(is.source_ref)}` : "created it" });
  if (is.start_requested_at) {
    out.push({
      at: is.start_requested_at,
      who: is.start_requested_by || undefined,
      what: is.start_requested_via ? `asked to start ${is.start_requested_via}, which covers it` : "asked to start it",
    });
  }
  if (is.spec_approved_at) out.push({ at: is.spec_approved_at, who: is.spec_approved_by || undefined, what: "approved the spec", tone: "ok" });
  if (is.spec_rejected_at) {
    out.push({
      at: is.spec_rejected_at,
      what: is.spec_feedback ? `The spec was sent back: ${is.spec_feedback}` : "The refinement failed; it is held until someone asks again",
      tone: "warn",
    });
  }
  for (const r of is.runs) {
    const name = `Run ${shortID(r.id)}`;
    out.push({ at: r.started_at ?? r.created_at, who: name, what: `started (${r.workflow})`, run: r.id });
    if (r.finished_at) {
      const e = ended[r.status] ?? { what: r.status.replaceAll("_", " "), tone: undefined };
      out.push({ at: r.finished_at, who: name, what: e.what, run: r.id, tone: e.tone });
    }
  }
  if (is.closed_at) {
    const verb = is.status === "canceled" ? "canceled it" : "completed it";
    out.push({ at: is.closed_at, who: is.closed_by || "someone", what: is.close_reason ? `${verb}: ${is.close_reason}` : verb, tone: is.status === "canceled" ? undefined : "ok" });
  }
  // Oldest first; entries at the same moment keep the order above (a run
  // starts before it ends).
  return out.map((e, i) => ({ e, i, t: new Date(e.at).getTime() }))
    .sort((a, b) => (a.t - b.t) || (a.i - b.i))
    .map((x) => x.e);
}
