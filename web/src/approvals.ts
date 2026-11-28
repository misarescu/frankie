// Human gate approvals as the control plane API returns them (internal/api/approvals.go).

export type Approval = {
  step_run_id: string;
  run_id: string;
  step: string;
  issue_key: string;
  issue_title: string;
  reason: string;
  commit_sha?: string;
  change_request_url?: string;
  expires_at: string;
  created_at: string;
  // The gate before a merge also takes "request-changes" (D92).
  can_request_changes?: boolean;
};

export type Decision = "approve" | "reject" | "request-changes";

// approvalHelp says what each decision does on a gate: before a merge,
// rejecting closes the change request and requesting changes sends it back.
export function approvalHelp(a: Pick<Approval, "can_request_changes">): { placeholder: string; reject: string } {
  if (!a.can_request_changes) return { placeholder: "Comment (optional; recorded with the decision)", reject: "Reject" };
  return {
    placeholder: "Comment (required to request changes: the coder gets it; with Reject it goes on the closed change request)",
    reject: "Reject and close",
  };
}

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

// decide sends a decision on a human gate. The control plane answers 202 once
// the run's workflow has the signal; the gate then leaves the inbox.
export async function decide(fetchFn: Fetch, gateID: string, decision: Decision, comment: string): Promise<void> {
  const r = await fetchFn(`/api/gates/${encodeURIComponent(gateID)}/decision`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ decision, comment: comment.trim() }),
  });
  if (r.status === 409) throw new Error("someone already decided this gate");
  if (r.status !== 202) throw new Error((await r.text()).trim() || `status ${r.status}`);
}

// expiresIn says how long a gate waits before its timeout applies.
export function expiresIn(expiresAt: string, now: Date = new Date()): string {
  const minutes = Math.round((new Date(expiresAt).getTime() - now.getTime()) / 60000);
  if (minutes <= 0) return "expired";
  if (minutes < 60) return `${minutes} min`;
  const hours = Math.round(minutes / 60);
  return hours < 48 ? `${hours} h` : `${Math.round(hours / 24)} days`;
}
