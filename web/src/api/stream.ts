// A run's event stream (GET /api/runs/{id}/stream, SSE) into the query cache
// (D141): one EventSource per run, shared by every component that follows the
// run (the issue page's run card and the run page), appending each event to
// ['run', id, 'events'] once, and refreshing what a state change makes stale.

import { useQuery, useQueryClient, type QueryClient, type QueryKey } from "@tanstack/react-query";
import { useEffect } from "react";
import { eventTypes, isConnectionError, parseStreamEvent, type RunEvent } from "@/events";
import { keys, noEvents, type RunEvents } from "./queries";

// mergeEvent adds an event to what the stream delivered, in seq order, once:
// a reconnect or a second subscriber may deliver it again.
export function mergeEvent(prev: RunEvents, e: RunEvent): RunEvents {
  const list = prev.events;
  const last = list.at(-1);
  if (!last || last.seq < e.seq) return { ...prev, events: [...list, e], reconnecting: false };
  if (list.some((x) => x.seq === e.seq)) return prev;
  return { ...prev, events: [...list, e].sort((a, b) => a.seq - b.seq), reconnecting: false };
}

// invalidations lists the queries an event makes stale: a state change moves
// a step, a run, an issue or a release, and the run's end settles all of them.
export function invalidations(runID: string, type: string): QueryKey[] {
  if (type !== "state_change" && type !== "end") return [];
  return [keys.runSteps(runID), ["issue"], ["issues"], keys.runs, ["release"], ["releases"], keys.approvals, keys.questions];
}

type Source = Pick<EventSource, "addEventListener" | "close">;
type SourceFactory = (url: string) => Source;

const open = new Map<string, { source: Source; refs: number }>();

// subscribeRun follows a run's stream until the returned function is called.
// Subscribers of one run share one connection; the last one to leave closes
// it. A new connection starts after the last event the cache already has.
export function subscribeRun(qc: QueryClient, runID: string, connect: SourceFactory = (u) => new EventSource(u)): () => void {
  const held = open.get(runID);
  if (held) {
    held.refs++;
    return () => release(runID);
  }
  const key = keys.runEvents(runID);
  const cached = qc.getQueryData<RunEvents>(key);
  if (cached?.end) return () => {};
  const after = cached?.events.at(-1)?.seq;
  const source = connect(`/api/runs/${encodeURIComponent(runID)}/stream${after ? `?after=${after}` : ""}`);
  open.set(runID, { source, refs: 1 });
  const update = (f: (prev: RunEvents) => RunEvents) => qc.setQueryData<RunEvents>(key, (prev) => f(prev ?? noEvents));
  const stale = (type: string) => {
    for (const k of invalidations(runID, type)) void qc.invalidateQueries({ queryKey: k });
  };
  for (const t of eventTypes) {
    source.addEventListener(t, (m: Event) => {
      const e = parseStreamEvent(m);
      if (e) {
        update((prev) => mergeEvent(prev, e));
        stale(e.type);
      } else if (t === "error" && isConnectionError(m)) {
        update((prev) => ({ ...prev, reconnecting: true }));
      }
    });
  }
  source.addEventListener("end", (m: Event) => {
    const e = parseStreamEvent(m) as { status?: string } | null;
    update((prev) => ({ ...prev, end: e?.status ?? "finished", reconnecting: false }));
    stale("end");
    source.close();
    open.delete(runID);
  });
  return () => release(runID);
}

function release(runID: string) {
  const held = open.get(runID);
  if (!held) return;
  if (--held.refs > 0) return;
  held.source.close();
  open.delete(runID);
}

// useRunEvents follows a run's transcript as it streams in. Without an id it
// follows nothing.
export function useRunEvents(runID: string | undefined): RunEvents {
  const qc = useQueryClient();
  useEffect(() => (runID ? subscribeRun(qc, runID) : undefined), [qc, runID]);
  const { data } = useQuery({
    queryKey: keys.runEvents(runID ?? ""),
    queryFn: () => qc.getQueryData<RunEvents>(keys.runEvents(runID ?? "")) ?? noEvents,
    enabled: !!runID,
    staleTime: Infinity,
    gcTime: 10 * 60_000,
  });
  return data ?? noEvents;
}
