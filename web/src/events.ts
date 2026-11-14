// Run transcript events as the control plane API returns them (internal/api).
export type RunEvent = {
  seq: number;
  time: string;
  type: string;
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
  "token_usage", "error", "done", "gate_result", "state_change", "intervention", "question",
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

// summary renders one event as a line of text for the transcript.
export function summary(e: RunEvent): string {
  const p = e.payload;
  switch (e.type) {
    case "assistant_message": return str(p.text);
    case "tool_call": return `${str(p.name)} ${str(p.input)}`;
    case "command_output": return str(p.output);
    case "tool_result": return str(p.content);
    case "file_diff": return `${str(p.path)}\n${str(p.diff)}`;
    case "token_usage": return `$${Number(p.cost_usd ?? 0).toFixed(4)} · ${str(p.turns)} turns · ${str(p.output_tokens)} output tokens`;
    case "state_change": return `${str(p.entity)}: ${str(p.from)} → ${str(p.to)}`;
    case "done": return [str(p.status), p.reason ? `: ${str(p.reason)}` : "", p.commit_sha ? ` · ${str(p.commit_sha)}` : ""].join("");
    case "error": return p.detail ? `${str(p.message)}: ${str(p.detail)}` : str(p.message);
    case "question": return `asked: ${str(p.question)}`;
    case "intervention": return `${str(p.by)} ${p.action === "abort" ? "aborted" : "steered"}: ${str(p.text)}${p.delivered === false ? " (the session had already ended)" : ""}`;
    default: return str(p);
  }
}
