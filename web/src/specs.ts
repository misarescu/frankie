// Specs waiting for a person's approval before their issue runs, as the
// control plane API returns them (internal/api/specs.go, D64).
export type Spec = {
  key: string;
  type: string;
  title: string;
  project: string;
  spec: string;
  acceptance_criteria: string[];
  file_hints: string[];
  labels: string[];
  source_ref?: string;
  requested_by?: string;
};

export type SpecDecision = "approve" | "reject";

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

// decideSpec approves a spec (the issue becomes ready and the scheduler starts
// it once its dependencies are done) or rejects it with what to change (the
// issue waits until its text changes or someone starts it again).
export async function decideSpec(fetchFn: Fetch, key: string, decision: SpecDecision, comment: string): Promise<void> {
  const c = comment.trim();
  if (decision === "reject" && c === "") throw new Error("say what to change before rejecting");
  const r = await fetchFn(`/api/issues/${encodeURIComponent(key)}/spec`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ decision, comment: c }),
  });
  if (r.status !== 200) throw new Error((await r.text()).trim() || `status ${r.status}`);
}
