// The run page's transcript (M9, D67): which step a page shows, the events
// of that step, and the rows they read as, with each tool call joined to its
// result so the page can fold it.

import type { Crumb } from "./crumbs";
import type { RunEvent } from "./events";
import { shortID } from "./format";
import { paths } from "./paths";
import { isActiveRun } from "./runs";
import { stepName, type Step } from "./steps";

// RUN_SECTION is the selection that shows the run's own events: those no
// step produced (the run's and its issue's status, a run-level error).
export const RUN_SECTION = "run";

// selectedStepID is the step the page shows: the one the URL names when the
// run has it, else the running step (the latest, for a parallel group), else
// the last step that ran, else the first planned one.
export function selectedStepID(timeline: Step[], requested: string | undefined, hasRunEvents: boolean): string | undefined {
  if (requested && (timeline.some((s) => s.id === requested) || (requested === RUN_SECTION && hasRunEvents))) return requested;
  const ran = timeline.filter((s) => !s.id.startsWith("plan-"));
  const running = ran.filter((s) => s.status === "running").at(-1);
  return (running ?? ran.at(-1) ?? timeline[0])?.id ?? (hasRunEvents ? RUN_SECTION : undefined);
}

// hasRun: the step executed (a skipped or not reached step comes from the plan).
export const hasRun = (s: Step): boolean => !s.id.startsWith("plan-");

// stepEvents are the events of one step execution, or the run's own.
export function stepEvents(events: RunEvent[], stepID: string): RunEvent[] {
  return stepID === RUN_SECTION ? events.filter((e) => !e.step_run_id) : events.filter((e) => e.step_run_id === stepID);
}

// loopBack says why the run went back to a step: the gate right before it
// failed and sent the run back ("looped back after checks failed"). The
// steps after it run again too; their attempt number says so.
export function loopBack(timeline: Step[], index: number): string | undefined {
  const s = timeline[index];
  const p = timeline.slice(0, index).filter(hasRun).at(-1);
  if (!s || s.attempt <= 1 || !p || p.kind !== "gate" || p.status !== "failed") return undefined;
  return `looped back after ${stepName(p)} failed`;
}

// A row of the transcript: one event, or a tool call with its result (a
// tool_result, or command_output for a shell call) once it arrived.
export type Row =
  | { kind: "event"; event: RunEvent }
  | { kind: "tool"; call: RunEvent; result?: RunEvent };

const str = (v: unknown): string => (typeof v === "string" ? v : "");

// rows joins each tool call to its result: the result naming the call's id,
// or, from a harness that sends no ids, the oldest call still waiting. A
// result with no call shows on its own.
export function rows(events: RunEvent[]): Row[] {
  const out: Row[] = [];
  const waiting: Extract<Row, { kind: "tool" }>[] = [];
  for (const e of events) {
    if (e.type === "tool_call") {
      const row = { kind: "tool" as const, call: e };
      out.push(row);
      waiting.push(row);
      continue;
    }
    if (e.type === "tool_result" || e.type === "command_output") {
      const id = str(e.payload.tool_call_id);
      const at = id ? waiting.findIndex((r) => str(r.call.payload.id) === id) : 0;
      const row = at >= 0 ? waiting[at] : undefined;
      if (row) {
        row.result = e;
        waiting.splice(at, 1);
        continue;
      }
    }
    out.push({ kind: "event", event: e });
  }
  return out;
}

// isShell: the row is a shell command's output, which the transcript shows
// open; other tool calls fold.
export const isShell = (r: Extract<Row, { kind: "tool" }>): boolean =>
  r.result?.type === "command_output" || /^(bash|shell)$/i.test(str(r.call.payload.name));

// isError: the tool's result reports a failure.
export const isError = (e: RunEvent | undefined): boolean => e?.payload.is_error === true;

// atBottom: the transcript is scrolled to its end (within a few pixels), so
// new events keep it there; scrolled up, it stays where the person put it.
export const atBottom = (scrollTop: number, clientHeight: number, scrollHeight: number, slack = 24): boolean =>
  scrollHeight - scrollTop - clientHeight <= slack;

// diffLine classifies a line of a unified diff for its color.
export function diffLine(line: string): "add" | "del" | "hunk" | "context" {
  if (line.startsWith("@@")) return "hunk";
  if (line.startsWith("+") && !line.startsWith("+++")) return "add";
  if (line.startsWith("-") && !line.startsWith("---")) return "del";
  return "context";
}

// Connection is what the page says about the transcript's stream.
// "connecting" until the run's status is known (GET /api/runs) or its stream
// ended: an unknown run is not called live.
export type Connection = "connecting" | "live" | "reconnecting" | "finished";

export function connection(runStatus: string | undefined, stream: { end: string | null; reconnecting: boolean }): Connection {
  if (stream.end || (runStatus && !isActiveRun(runStatus))) return "finished";
  if (!runStatus) return "connecting";
  return stream.reconnecting ? "reconnecting" : "live";
}

// runCrumbs are the crumbs after the issue (or the release): the run, then
// the selected step.
export function runCrumbs(runID: string, step: { id: string; name: string } | undefined): Crumb[] {
  const run: Crumb = { label: `Run ${shortID(runID)}`, ...(step ? { href: paths.run(runID) } : {}) };
  return step ? [run, { label: step.name }] : [run];
}

// releaseRunCrumbs are a release run's crumbs: project > Releases > release.
export function releaseRunCrumbs(project: { title: string; prefix: string } | undefined, release: { id: string; name: string }): Crumb[] {
  return [
    ...(project ? [{ label: project.title, href: paths.board(project.prefix) }, { label: "Releases", href: paths.releases(project.prefix) }] : []),
    { label: `Release ${release.name}`, href: paths.release(release.id) },
  ];
}
