// Run transcript events as the control plane API returns them (internal/api).
export type RunEvent = {
  seq: number;
  time: string;
  type: string;
  // The step execution the event belongs to; run-level events have none.
  step_run_id?: string;
  payload: Record<string, unknown>;
};

export type RunSummary = {
  ID: string;
  Status: string;
  IssueKey: string;
  IssueTitle: string;
  CreatedAt: string;
  CostUSD: number;
};

export const eventTypes = [
  "assistant_message", "tool_call", "tool_result", "file_diff", "command_output",
  "token_usage", "error", "done", "gate_result", "state_change", "intervention", "question", "image_build", "build_log",
] as const;

// The transcript has an event type named "error", which is also the name of
// EventSource's own connection-error event (no data). Only a MessageEvent with
// string data is a transcript event; anything else under that name means the
// connection dropped and EventSource is reconnecting.

// parseStreamEvent returns the transcript event an SSE message carries, or
// null for EventSource's own events.
export function parseStreamEvent(m: Event): RunEvent | null {
  if (!(m instanceof MessageEvent) || typeof m.data !== "string") return null;
  try {
    return JSON.parse(m.data) as RunEvent;
  } catch {
    return null;
  }
}

// isConnectionError tells EventSource's connection errors from a transcript
// event of type "error", which also reaches es.onerror.
export const isConnectionError = (e: Event): boolean => !(e instanceof MessageEvent);

const str = (v: unknown): string => (v === undefined || v === null ? "" : typeof v === "string" ? v : JSON.stringify(v));

const entities: Record<string, string> = { issue: "Issue", run: "Run", step_run: "Step", change_request: "Change request" };

// label names an event in plain words (M5.1) instead of its wire type.
export function label(e: RunEvent): string {
  const p = e.payload;
  switch (e.type) {
    case "assistant_message": return "Agent";
    case "tool_call": return p.name ? `Tool: ${str(p.name)}` : "Tool call";
    case "tool_result": return "Tool result";
    case "file_diff": return "File changed";
    case "command_output": return "Command output";
    case "token_usage": return "Model usage";
    case "error": return "Error";
    case "done": return "Session ended";
    case "gate_result": return "Gate result";
    case "state_change": return "Status";
    case "question": return "Question for a person";
    case "image_build": return "Image build";
    case "build_log": return "Build log";
    case "intervention": return p.action === "abort" ? "Abort" : p.action === "answer" ? "Answer" : "Steer";
    default: return e.type.replaceAll("_", " ");
  }
}

// toolInput shows the part of a tool call's input a person reads first: the
// command, the file or the pattern; otherwise the whole input.
function toolInput(input: unknown): string {
  if (input && typeof input === "object") {
    const o = input as Record<string, unknown>;
    for (const k of ["command", "file_path", "path", "pattern", "url", "query"]) {
      if (typeof o[k] === "string" && o[k] !== "") return o[k] as string;
    }
  }
  return str(input);
}

const verdicts: Record<string, string> = { pass: "passed", fail: "failed", escalated: "escalated to a person" };

// gateSummary says how a gate ended and why: each command's exit code, or each
// reviewer's verdict.
function gateSummary(p: Record<string, unknown>): string {
  const head = `${str(p.type)} gate ${verdicts[str(p.verdict)] ?? str(p.verdict)}`;
  const d = (p.details ?? {}) as { error?: string; commands?: { command: string; exitCode?: number | null; reason?: string }[];
    reviews?: { role?: string; step?: string; verdict?: string; summary?: string; error?: string }[];
    checks?: { name: string; state: string; summary?: string; url?: string }[]; state?: string };
  const lines: string[] = [];
  if (d.error) lines.push(d.error);
  for (const c of d.commands ?? []) {
    lines.push(`${c.command}: ${c.exitCode === undefined || c.exitCode === null ? "did not run" : `exit ${c.exitCode}`}${c.reason ? ` (${c.reason})` : ""}`);
  }
  for (const r of d.reviews ?? []) {
    const v = r.error ? `no verdict: ${r.error}` : `${r.verdict ?? ""}${r.summary ? `: ${r.summary}` : ""}`;
    lines.push(`${r.role || r.step || "reviewer"} ${v}`);
  }
  // The repo's own CI (D90): failed checks first, each with why and where.
  if (p.type === "provider-checks") {
    if (d.state === "none") lines.push("the repo has no CI checks on this commit");
    const order = (c: { state: string }) => (c.state === "failure" ? 0 : c.state === "pending" ? 1 : 2);
    for (const c of [...(d.checks ?? [])].sort((a, b) => order(a) - order(b))) {
      lines.push(`${c.name}: ${c.state}${c.summary ? ` (${c.summary})` : ""}${c.url && c.state === "failure" ? ` ${c.url}` : ""}`);
    }
  }
  return [head, ...lines].join("\n");
}

// summary renders one event as a line of text for the transcript.
export function summary(e: RunEvent): string {
  const p = e.payload;
  switch (e.type) {
    case "assistant_message": return str(p.text);
    case "tool_call": return toolInput(p.input);
    case "command_output": return str(p.output);
    case "tool_result": return str(p.content);
    case "file_diff": return `${str(p.path)}\n${str(p.diff)}`;
    case "token_usage": return `$${Number(p.cost_usd ?? 0).toFixed(4)} · ${str(p.turns)} turns · ${str(p.output_tokens)} output tokens`;
    case "state_change": return `${entities[str(p.entity)] ?? str(p.entity)}: ${str(p.from)} → ${str(p.to)}`;
    case "gate_result": return gateSummary(p);
    case "done": return [str(p.status), p.reason ? `: ${str(p.reason)}` : "", p.commit_sha ? ` · ${str(p.commit_sha)}` : ""].join("");
    case "error": return p.detail ? `${str(p.message)}: ${str(p.detail)}` : str(p.message);
    case "question": return `asked: ${str(p.question)}`;
    case "image_build": {
      // D93: a step waits for its image; the build's log follows as build_log.
      const image = p.image === "dependencies" ? "dependency" : "toolchain";
      if (p.status === "building") return `building the ${image} image ${str(p.tag)}`;
      const took = p.seconds ? ` in ${Math.round(Number(p.seconds))}s` : "";
      return p.status === "failed" ? `the ${image} image build failed${took}: ${str(p.error)}` : `built the ${image} image${took}`;
    }
    case "build_log": return str(p.output);
    case "intervention": {
      const verb = p.action === "abort" ? "aborted" : p.action === "answer" ? "answered" : "steered";
      const missed = p.action === "answer" ? " (the agent no longer waited for it)" : " (the session had already ended)";
      return `${str(p.by)} ${verb}: ${str(p.text)}${p.delivered === false ? missed : ""}`;
    }
    default: return str(p);
  }
}

// A segment is a stretch of consecutive events of one step execution, or of the
// run itself, so the transcript can say once which step, role, harness and model
// produced them.
export type Segment = { stepRunID?: string; events: RunEvent[] };

export function segments(events: RunEvent[]): Segment[] {
  const out: Segment[] = [];
  for (const e of events) {
    const last = out.at(-1);
    if (last && last.stepRunID === e.step_run_id) last.events.push(e);
    else out.push({ stepRunID: e.step_run_id, events: [e] });
  }
  return out;
}
