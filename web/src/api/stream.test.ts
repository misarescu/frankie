import { expect, test } from "bun:test";
import { QueryClient } from "@tanstack/react-query";
import type { RunEvent } from "@/events";
import { keys, noEvents, type RunEvents } from "./queries";
import { invalidations, mergeEvent, subscribeRun } from "./stream";

const ev = (seq: number, type = "assistant_message"): RunEvent => ({ seq, time: "2026-11-28T10:00:00Z", type, payload: {} });
const seqs = (r: RunEvents) => r.events.map((e) => e.seq);

test("mergeEvent appends in order and drops an event it already has", () => {
  let r = mergeEvent(noEvents, ev(1));
  r = mergeEvent(r, ev(2));
  expect(seqs(r)).toEqual([1, 2]);
  expect(mergeEvent(r, ev(2))).toBe(r); // a reconnect delivers it again
  expect(seqs(mergeEvent(mergeEvent(r, ev(5)), ev(3)))).toEqual([1, 2, 3, 5]);
});

test("an event clears the reconnecting note", () => {
  expect(mergeEvent({ ...noEvents, reconnecting: true }, ev(1)).reconnecting).toBe(false);
});

test("a state change and the end make steps, runs, issues and releases stale; other events nothing", () => {
  expect(invalidations("r1", "assistant_message")).toEqual([]);
  const k = invalidations("r1", "state_change");
  expect(k).toContainEqual(["run", "r1", "steps"]);
  for (const prefix of [["issue"], ["issues"], ["runs"], ["release"], ["releases"]]) expect(k).toContainEqual(prefix);
  expect(invalidations("r1", "end")).toEqual(k);
});

// A fake EventSource the test drives.
class FakeSource {
  static made: FakeSource[] = [];
  listeners = new Map<string, ((m: Event) => void)[]>();
  closed = false;
  constructor(readonly url: string) { FakeSource.made.push(this); }
  addEventListener(t: string, f: (m: Event) => void) { this.listeners.set(t, [...(this.listeners.get(t) ?? []), f]); }
  close() { this.closed = true; }
  send(type: string, data: unknown) {
    for (const f of this.listeners.get(type) ?? []) f(new MessageEvent(type, { data: JSON.stringify(data) }));
  }
  drop() { for (const f of this.listeners.get("error") ?? []) f(new Event("error")); }
}
const connect = (u: string) => new FakeSource(u);

test("subscribers of one run share one stream, closed when the last leaves", () => {
  FakeSource.made = [];
  const qc = new QueryClient();
  const a = subscribeRun(qc, "r1", connect);
  const b = subscribeRun(qc, "r1", connect);
  expect(FakeSource.made.length).toBe(1);
  expect(FakeSource.made[0]!.url).toBe("/api/runs/r1/stream");
  a();
  expect(FakeSource.made[0]!.closed).toBe(false);
  b();
  expect(FakeSource.made[0]!.closed).toBe(true);
});

test("events land in the cache once; a reconnect resumes after the last one", () => {
  FakeSource.made = [];
  const qc = new QueryClient();
  const stop = subscribeRun(qc, "r2", connect);
  const s = FakeSource.made[0]!;
  s.send("tool_call", ev(1, "tool_call"));
  s.send("tool_call", ev(1, "tool_call"));
  s.drop();
  expect(qc.getQueryData<RunEvents>(keys.runEvents("r2"))!.reconnecting).toBe(true);
  s.send("error", ev(2, "error")); // a transcript event named "error" is an event, not a drop
  const got = qc.getQueryData<RunEvents>(keys.runEvents("r2"))!;
  expect(seqs(got)).toEqual([1, 2]);
  expect(got.reconnecting).toBe(false);
  stop();
  const again = subscribeRun(qc, "r2", connect);
  expect(FakeSource.made[1]!.url).toBe("/api/runs/r2/stream?after=2");
  again();
});

test("the end records the outcome, closes the stream and refreshes the run's steps", async () => {
  FakeSource.made = [];
  const qc = new QueryClient();
  let fetched = 0;
  await qc.prefetchQuery({ queryKey: keys.runSteps("r3"), queryFn: () => ++fetched });
  const observer = qc.getQueryCache().find({ queryKey: keys.runSteps("r3") })!;
  expect(observer.state.isInvalidated).toBe(false);
  subscribeRun(qc, "r3", connect);
  const s = FakeSource.made[0]!;
  s.send("state_change", ev(1, "state_change"));
  expect(observer.state.isInvalidated).toBe(true);
  s.send("end", { status: "succeeded" });
  expect(s.closed).toBe(true);
  expect(qc.getQueryData<RunEvents>(keys.runEvents("r3"))!.end).toBe("succeeded");
  // A run that ended opens no new stream.
  subscribeRun(qc, "r3", connect)();
  expect(FakeSource.made.length).toBe(1);
});
