// A run's step executions as the control plane API returns them (internal/api/steps.go).
export type Step = {
  id: string;
  step_id: string;
  attempt: number;
  kind: string;
  status: string;
  commit_sha?: string;
  started_at?: string;
  finished_at?: string;
};

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

// runningAgentStep is the agent step a person can steer or abort, if any:
// the latest running one (parallel branches run one at a time, D42).
export function runningAgentStep(steps: Step[]): Step | undefined {
  return steps.filter((s) => s.kind === "agent" && s.status === "running").at(-1);
}

async function post(fetchFn: Fetch, url: string, body: Record<string, string>): Promise<void> {
  const r = await fetchFn(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (r.status === 409) throw new Error("the step is no longer running");
  if (r.status !== 202) throw new Error((await r.text()).trim() || `status ${r.status}`);
}

// steer sends a message to a running agent step; the agent gets it at its
// next model call (docs/spec/04, "Human intervention").
export function steer(fetchFn: Fetch, stepID: string, text: string): Promise<void> {
  return post(fetchFn, `/api/steps/${encodeURIComponent(stepID)}/steer`, { text: text.trim() });
}

// abort stops a running agent step; the run fails naming the reason.
export function abort(fetchFn: Fetch, stepID: string, reason: string): Promise<void> {
  return post(fetchFn, `/api/steps/${encodeURIComponent(stepID)}/abort`, { reason: reason.trim() });
}
