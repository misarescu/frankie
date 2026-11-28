// A run's step executions as the control plane API returns them (internal/api/steps.go).

import { signed } from "./identity";

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
  // What a step waits for: a person (M5.1), a human gate's decision or an
  // answer to the agent's question; or the build of its image (D93).
  waits_for?: "approval" | "answer" | "toolchain-image" | "dependency-image";
  // Why a step the run went past did not run: its condition (D98).
  skipped_because?: string;
};

const buildingImage = (s: Step) => s.waits_for === "toolchain-image" || s.waits_for === "dependency-image";

// shownStatus is the status to show for a step: a running step that waits for
// a person shows as awaiting a human, like an issue does; a step whose image
// is being built says so.
export const shownStatus = (s: Step): string => {
  if (buildingImage(s) && (s.status === "pending" || s.status === "running")) return "building_image";
  return s.status === "running" && s.waits_for ? "awaiting_human" : s.status;
};

// waitNote says what a waiting step waits for and where to look or act: a
// person acts in Approvals; a build's log is in the run's transcript, at
// runHref when the page is not the run's own.
export function waitNote(s: Step, runHref?: string): { text: string; href?: string } | undefined {
  if (buildingImage(s) && (s.status === "pending" || s.status === "running")) {
    const image = s.waits_for === "dependency-image" ? "dependency" : "toolchain";
    return runHref ? { text: `building the ${image} image: see its log`, href: runHref } : { text: `building the ${image} image; its log is below` };
  }
  if (s.status !== "running") return undefined;
  if (s.waits_for === "approval") return { text: "decide it in Approvals", href: "/approvals?kind=gate" };
  if (s.waits_for === "answer") return { text: "answer it in Approvals", href: "/approvals?kind=question" };
  return undefined;
}

// When is a step's condition (D94): it runs only if the named earlier step's
// result has that value.
export type When = { step: string; field: string; equals: boolean | string };

// PlanStep is a step a run's workflow executes, in order (GET /api/runs/{id}/plan).
export type PlanStep = { step_id: string; kind: string; role?: string; harness?: string; model?: string; gate?: string; when?: When };

// condition says when a conditional step runs: "runs only when investigate's testable is false".
export const condition = (w: When): string => `runs only when ${w.step}'s ${w.field} is ${String(w.equals)}`;

// timeline lists a run's steps for its timeline: the step executions in the
// order they ran, with the planned steps that did not run (D98). A
// conditional step the run went past (a later step in the plan ran) shows as
// skipped, with its condition, where it would have run. While the run goes
// on, the steps not reached yet follow, greyed, in plan order; once it ends,
// they are left out, since it will not reach them.
export function timeline(plan: PlanStep[], ran: Step[], ended: boolean): Step[] {
  const index = new Map(plan.map((p, i) => [p.step_id, i]));
  const seen = new Set(ran.map((s) => s.step_id));
  const last = Math.max(-1, ...ran.map((s) => index.get(s.step_id) ?? -1));
  const unrun = (p: PlanStep, status: string): Step => {
    const { when, ...rest } = p;
    return {
      ...rest, id: `plan-${p.step_id}`, attempt: 1, status, cost_usd: 0, input_tokens: 0, output_tokens: 0,
      ...(status === "skipped" && when ? { skipped_because: condition(when) } : {}),
    };
  };
  const skipped = plan.flatMap((p, i) => (!seen.has(p.step_id) && p.when && i < last ? [{ at: i, step: unrun(p, "skipped") }] : []));
  const out: Step[] = [];
  for (const s of ran) {
    const at = index.get(s.step_id) ?? -1;
    for (let k = skipped[0]; k && k.at < at; k = skipped[0]) {
      out.push(k.step);
      skipped.shift();
    }
    out.push(s);
  }
  out.push(...skipped.map((k) => k.step));
  if (!ended) {
    out.push(...plan.filter((p, i) => !seen.has(p.step_id) && !(p.when && i < last)).map((p) => unrun(p, "not started")));
  }
  return out;
}

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

async function post(fetchFn: Fetch, url: string, body: Record<string, string>, by?: string | null): Promise<void> {
  const r = await fetchFn(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(signed(body, by)),
  });
  if (r.status === 409) throw new Error("the step is no longer running");
  if (r.status !== 202) throw new Error((await r.text()).trim() || `status ${r.status}`);
}

// steer sends a message to a running agent step; the agent gets it at its
// next model call (docs/spec/04, "Human intervention").
export function steer(fetchFn: Fetch, stepID: string, text: string, by?: string | null): Promise<void> {
  return post(fetchFn, `/api/steps/${encodeURIComponent(stepID)}/steer`, { text: text.trim() }, by);
}

// abort stops a running agent step; the run fails naming the reason.
export function abort(fetchFn: Fetch, stepID: string, reason: string, by?: string | null): Promise<void> {
  return post(fetchFn, `/api/steps/${encodeURIComponent(stepID)}/abort`, { reason: reason.trim() }, by);
}
