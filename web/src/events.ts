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
};

export const eventTypes = [
  "assistant_message", "tool_call", "tool_result", "file_diff", "command_output",
  "token_usage", "error", "done", "gate_result", "state_change",
] as const;

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
    default: return str(p);
  }
}
