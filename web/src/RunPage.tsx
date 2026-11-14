import { useCallback, useEffect, useRef, useState } from "react";
import { eventTypes, isConnectionError, parseStreamEvent, summary, type RunEvent } from "./events";
import { abort, runningAgentStep, steer, type Step } from "./steps";

// Live transcript: the control plane's SSE stream replays stored events, then
// follows live ones; EventSource resumes with Last-Event-ID after a drop.
export function RunPage({ runID }: { runID: string }) {
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [status, setStatus] = useState("connecting…");
  const bottom = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setEvents([]);
    const es = new EventSource(`/api/runs/${runID}/stream`);
    es.onopen = () => setStatus("live");
    es.onerror = (e) => {
      if (isConnectionError(e)) setStatus("reconnecting…");
    };
    es.addEventListener("end", (m) => {
      const end = parseStreamEvent(m) as { status?: string } | null;
      setStatus(`finished: ${end?.status ?? "unknown"}`);
      es.close();
    });
    for (const t of eventTypes) {
      es.addEventListener(t, (m) => {
        const e = parseStreamEvent(m);
        if (e) setEvents((prev) => [...prev, e]);
      });
    }
    return () => es.close();
  }, [runID]);

  useEffect(() => bottom.current?.scrollIntoView({ block: "end" }), [events.length]);

  // Which agent step is running, for steering or aborting it; refreshed while
  // the run is live.
  const [steps, setSteps] = useState<Step[]>([]);
  const loadSteps = useCallback(() => {
    fetch(`/api/runs/${runID}/steps`)
      .then((r) => (r.ok ? r.json() : []))
      .then((data: Step[]) => setSteps(data))
      .catch(() => {});
  }, [runID]);
  const finished = status.startsWith("finished");
  useEffect(() => {
    loadSteps();
    if (finished) return;
    const t = setInterval(loadSteps, 5000);
    return () => clearInterval(t);
  }, [loadSteps, finished]);
  const running = finished ? undefined : runningAgentStep(steps);

  return (
    <>
      <h1><a href="#/">Runs</a> / {runID}</h1>
      <div className="status">{status}</div>
      {running && <InterventionPanel key={running.id} step={running} onSent={loadSteps} />}
      {events.map((e) => (
        <div className="ev" key={e.seq}>
          <span className="type">#{e.seq} {e.type}</span>
          <pre>{summary(e)}</pre>
        </div>
      ))}
      <div ref={bottom} />
    </>
  );
}

// InterventionPanel steers or aborts the running agent step (docs/spec/04,
// "Human intervention"). A steer reaches the agent at its next model call; an
// abort stops the step and fails the run.
function InterventionPanel({ step, onSent }: { step: Step; onSent: () => void }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const run = (action: () => Promise<void>, done: string) => {
    setBusy(true);
    setNote(null);
    action()
      .then(() => { setText(""); setNote(done); onSent(); })
      .catch((e: Error) => setNote(e.message))
      .finally(() => setBusy(false));
  };
  return (
    <section className="approval">
      <h2>Running: {step.step_id} <span className="type">· attempt {step.attempt}</span></h2>
      <textarea placeholder="Message to the agent (steer), or the reason for aborting" value={text} maxLength={8000}
        onChange={(e) => setText(e.target.value)} disabled={busy} />
      <div className="actions">
        <button onClick={() => run(() => steer(fetch, step.id, text), "sent; the agent gets it at its next model call")}
          disabled={busy || !text.trim()}>Steer</button>
        <button className="reject" onClick={() => {
          if (window.confirm(`Abort ${step.step_id}? The run fails.`)) run(() => abort(fetch, step.id, text), "abort sent");
        }} disabled={busy || !text.trim()}>Abort</button>
        {note && <span className="status">{note}</span>}
      </div>
    </section>
  );
}
