import { useEffect, useRef, useState } from "react";
import { eventTypes, isConnectionError, parseStreamEvent, summary, type RunEvent } from "./events";

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

  return (
    <>
      <h1><a href="#/">Runs</a> / {runID}</h1>
      <div className="status">{status}</div>
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
