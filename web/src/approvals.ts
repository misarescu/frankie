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
};

export type Decision = "approve" | "reject";

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
