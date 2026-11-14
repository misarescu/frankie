// A run's step executions as the control plane API returns them (internal/api/steps.go).
export type Step = {
  id: string;
  step_id: string;
  attempt: number;
  kind: string;
  status: string;
  commit_sha?: string;
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
  started_at?: string;
  finished_at?: string;
  // What the run's snapshot says is behind the step (M5.1): an agent step's
  // role, harness and model (none when the platform's default applies), or a
  // gate's type.
  role?: string;
  harness?: string;
  model?: string;
  gate?: string;
};

// who says what does a step's work: "coder · claude-code · anthropic/claude-sonnet-5-5"
// for an agent, "command gate" for a gate.
export function who(s: Step): string {
  if (s.role) return [s.role, s.harness, s.model || "default model"].filter(Boolean).join(" · ");
  if (s.gate) return `${s.gate} gate`;
  if (s.kind === "action") return "action";
  return "";
}

// stepName names a step execution: its step and, after a loop, its attempt.
export const stepName = (s: Step): string => (s.attempt > 1 ? `${s.step_id} (attempt ${s.attempt})` : s.step_id);

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

// usd renders a cost; spend under a cent still shows.
export const usd = (n: number): string => (n > 0 && n < 0.01 ? "<$0.01" : `$${n.toFixed(2)}`);

// totals adds up the model usage of a run's steps (D53).
export function totals(steps: Step[]): { cost: number; input: number; output: number } {
  return steps.reduce((t, s) => ({ cost: t.cost + s.cost_usd, input: t.input + s.input_tokens, output: t.output + s.output_tokens }),
    { cost: 0, input: 0, output: 0 });
}

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
