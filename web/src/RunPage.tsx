import { useCallback, useEffect, useRef, useState } from "react";
import { eventTypes, isConnectionError, label, parseStreamEvent, segments, summary, type RunEvent } from "./events";
import { abort, runningAgentStep, steer, stepName, totals, usd, who, type Step } from "./steps";
import { apiFetch } from "./auth";

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
      {steps.length > 0 && <StepsTable steps={steps} />}
      {running && <InterventionPanel key={running.id} step={running} onSent={loadSteps} />}
      <Transcript events={events} steps={steps} />
      <div ref={bottom} />
    </>
  );
}

// Transcript shows the run's events in plain words, under a heading for each
// stretch of one step's events naming the step and its role, harness and model
// (M5.1). Events from a step the step list does not know yet still show, under
// a plain "Step" heading, until the next refresh names it.
function Transcript({ events, steps }: { events: RunEvent[]; steps: Step[] }) {
  const byID = new Map(steps.map((s) => [s.id, s]));
  return (
    <>
      {segments(events).map((seg) => {
        const step = seg.stepRunID ? byID.get(seg.stepRunID) : undefined;
        return (
          <section className="segment" key={seg.events[0]!.seq}>
            <h3 className="segment-head">
              {seg.stepRunID ? (step ? stepName(step) : "Step") : "Run"}
              {step && who(step) && <span className="type"> · {who(step)}</span>}
            </h3>
            {seg.events.map((e) => (
              <div className={`ev ev-${e.type}`} key={e.seq}>
                <span className="type" title={`#${e.seq} ${e.type} · ${new Date(e.time).toLocaleString()}`}>{label(e)}</span>
                <pre>{summary(e)}</pre>
              </div>
            ))}
          </section>
        );
      })}
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
        <button onClick={() => run(() => steer(apiFetch, step.id, text), "sent; the agent gets it at its next model call")}
          disabled={busy || !text.trim()}>Steer</button>
        <button className="reject" onClick={() => {
          if (window.confirm(`Abort ${step.step_id}? The run fails.`)) run(() => abort(apiFetch, step.id, text), "abort sent");
        }} disabled={busy || !text.trim()}>Abort</button>
        {note && <span className="status">{note}</span>}
      </div>
    </section>
  );
}

// StepsTable lists the run's step executions with their model usage (D53).
function StepsTable({ steps }: { steps: Step[] }) {
  const t = totals(steps);
  return (
    <table className="steps">
      <thead><tr><th>Step</th><th>By</th><th>Status</th><th>Cost</th><th>Tokens in / out</th></tr></thead>
      <tbody>
        {steps.map((s) => (
          <tr key={s.id}>
            <td>{stepName(s)}</td>
            <td className="type">{who(s)}</td>
            <td>{s.status}</td>
            <td>{s.kind === "agent" ? usd(s.cost_usd) : ""}</td>
            <td>{s.kind === "agent" ? `${s.input_tokens.toLocaleString()} / ${s.output_tokens.toLocaleString()}` : ""}</td>
          </tr>
        ))}
        <tr><td>Total</td><td /><td /><td>{usd(t.cost)}</td><td>{`${t.input.toLocaleString()} / ${t.output.toLocaleString()}`}</td></tr>
      </tbody>
    </table>
  );
}
