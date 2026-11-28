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
  return [keys.run(runID), keys.runSteps(runID), ["issue"], ["issues"], keys.runs, ["release"], ["releases"], keys.approvals, keys.questions];
}

type Source = { addEventListener(type: string, listener: (m: Event) => void): void; close(): void; readyState?: number };
type SourceFactory = (url: string) => Source;
type Held = { source: Source; refs: number; retry?: ReturnType<typeof setTimeout> };

// EventSource.CLOSED: the browser gave up on the stream, as it does when the
// server answers with an error (the control plane down) instead of the stream.
const CLOSED = 2;
export const RETRY_MS = 3000;
// A stream starts by replaying every stored event; their state changes come
// in a burst, and refreshing per event refetched each query dozens of times.
// Invalidations within this window are sent once, after it.
export const COALESCE_MS = 250;

const open = new Map<string, Held>();

// Invalidations wait in one batch per query client, shared by every stream:
// a parent's page follows several runs, whose replays would each refresh the
// same lists.
type Batch = { keys: Map<string, QueryKey>; timer?: ReturnType<typeof setTimeout> };
const batches = new WeakMap<QueryClient, Batch>();

function invalidateSoon(qc: QueryClient, ks: QueryKey[], coalesceMs: number) {
  const b: Batch = batches.get(qc) ?? { keys: new Map() };
  batches.set(qc, b);
  for (const k of ks) b.keys.set(JSON.stringify(k), k);
  if (b.keys.size === 0 || b.timer) return;
  b.timer = setTimeout(() => {
    b.timer = undefined;
    const due = [...b.keys.values()];
    b.keys.clear();
    for (const k of due) void qc.invalidateQueries({ queryKey: k });
  }, coalesceMs);
}

// subscribeRun follows a run's stream until the returned function is called.
// Subscribers of one run share one connection; the last one to leave closes
// it. A new connection starts after the last event the cache already has,
// also when the browser gave up on the old one: it opens again after retryMs.
export function subscribeRun(qc: QueryClient, runID: string, connect: SourceFactory = (u) => new EventSource(u), retryMs = RETRY_MS, coalesceMs = COALESCE_MS): () => void {
  const held = open.get(runID);
  if (held) {
    held.refs++;
    return () => release(runID);
  }
  const key = keys.runEvents(runID);
  if (qc.getQueryData<RunEvents>(key)?.end) return () => {};
  const update = (f: (prev: RunEvents) => RunEvents) => qc.setQueryData<RunEvents>(key, (prev) => f(prev ?? noEvents));
  const stale = (type: string) => invalidateSoon(qc, invalidations(runID, type), coalesceMs);
  const current = (source: Source) => open.get(runID)?.source === source;
  const start = (): Source => {
    const after = qc.getQueryData<RunEvents>(key)?.events.at(-1)?.seq;
    const source = connect(`/api/runs/${encodeURIComponent(runID)}/stream${after ? `?after=${after}` : ""}`);
    for (const t of eventTypes) {
      source.addEventListener(t, (m: Event) => {
        const e = parseStreamEvent(m);
        if (e) {
          update((prev) => mergeEvent(prev, e));
          stale(e.type);
        } else if (t === "error" && isConnectionError(m)) {
          update((prev) => ({ ...prev, reconnecting: true }));
          const h = open.get(runID);
          if (h && current(source) && source.readyState === CLOSED && !h.retry) {
            h.retry = setTimeout(() => {
              h.retry = undefined;
              if (current(source)) h.source = start();
            }, retryMs);
          }
        }
      });
    }
    // Connected (again): the stream is live, though no new event came yet.
    source.addEventListener("open", () => update((prev) => (prev.reconnecting ? { ...prev, reconnecting: false } : prev)));
    source.addEventListener("end", (m: Event) => {
      const e = parseStreamEvent(m) as { status?: string } | null;
      update((prev) => ({ ...prev, end: e?.status ?? "finished", reconnecting: false }));
      stale("end");
      source.close();
      if (current(source)) open.delete(runID);
    });
    return source;
  };
  const h: Held = { source: { addEventListener() {}, close() {} }, refs: 1 };
  open.set(runID, h);
  h.source = start();
  return () => release(runID);
}

function release(runID: string) {
  const held = open.get(runID);
  if (!held) return;
  if (--held.refs > 0) return;
  clearTimeout(held.retry);
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
