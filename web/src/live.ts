// The issue page's live view (M5.1): the step timeline of each run in progress
// under the issue and the agent's latest messages and tool calls. The full
// transcript stays on the run page.

import type { RunEvent } from "./events";

// A run in progress on the issue or on an issue under it (internal/api/issues.go).
export type LiveRunRef = {
  id: string;
  status: string;
  workflow: string;
  created_at: string;
  issue_key: string;
  issue_title: string;
};

// What the live view shows of a run's events: what the agent says and does, and
// what people and gates answer. Tool results, diffs and usage stay on the run page.
const activity = new Set(["assistant_message", "tool_call", "question", "intervention", "gate_result", "error"]);

export const LATEST = 6;

// keepLatest adds an event to the latest activity if it is activity, keeping
// the last n.
export function keepLatest(list: RunEvent[], e: RunEvent, n: number = LATEST): RunEvent[] {
  if (!activity.has(e.type)) return list;
  const out = [...list, e];
  return out.length > n ? out.slice(out.length - n) : out;
}

// duration says how long a step took, or has run so far.
export function duration(start: string | undefined, end: string | undefined, now: Date = new Date()): string {
  if (!start) return "";
  const ms = (end ? new Date(end) : now).getTime() - new Date(start).getTime();
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, "0")}s`;
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;
}

// watch merges the runs in progress into the ones the page already follows, so
// a run that finishes stays on screen with its outcome instead of vanishing.
// Newest last, at most max: each one holds an event stream open, and browsers
// allow six connections per origin.
export function watch(prev: LiveRunRef[], live: LiveRunRef[], max = 3): LiveRunRef[] {
  const byID = new Map(prev.map((r) => [r.id, r]));
  for (const r of live) byID.set(r.id, r);
  const out = [...byID.values()].sort((a, b) => a.created_at.localeCompare(b.created_at) || a.id.localeCompare(b.id));
  return out.slice(Math.max(0, out.length - max));
}

// clip shortens long text for the live view.
export function clip(text: string, max = 400): string {
  return text.length > max ? `${text.slice(0, max).trimEnd()} …` : text;
}
