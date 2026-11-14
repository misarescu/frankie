import { useEffect, useState } from "react";
import { eventTypes, label, parseStreamEvent, summary, type RunEvent } from "./events";
import { StatusChip } from "./IssuesPage";
import { clip, duration, keepLatest, type LiveRunRef } from "./live";
import { stepName, upcoming, usd, who, type PlanStep, type Step } from "./steps";

// LiveRun follows one run on the issue page (M5.1): its step timeline with
// status and duration, and the agent's latest messages and tool calls, from the
// same stream as the run page. The full transcript is one link away.
export function LiveRun({ run, showIssue }: { run: LiveRunRef; showIssue: boolean }) {
  const [latest, setLatest] = useState<RunEvent[]>([]);
  const [end, setEnd] = useState<string | null>(null);
  const [steps, setSteps] = useState<Step[]>([]);
  const [now, setNow] = useState(() => new Date());
  const [plan, setPlan] = useState<PlanStep[]>([]);

  // The workflow's steps, fixed for the run (invariant 4): read once.
  useEffect(() => {
    let alive = true;
    setPlan([]);
    fetch(`/api/runs/${run.id}/plan`)
      .then((r) => (r.ok ? r.json() : []))
      .then((d: PlanStep[]) => { if (alive) setPlan(d); })
      .catch(() => {});
    return () => { alive = false; };
  }, [run.id]);

  useEffect(() => {
    setLatest([]);
    setEnd(null);
    const es = new EventSource(`/api/runs/${run.id}/stream`);
    es.addEventListener("end", (m) => {
      const e = parseStreamEvent(m) as { status?: string } | null;
      setEnd(e?.status ?? "finished");
      es.close();
    });
    for (const t of eventTypes) {
      es.addEventListener(t, (m) => {
        const e = parseStreamEvent(m);
        if (e) setLatest((prev) => keepLatest(prev, e));
      });
    }
    return () => es.close();
  }, [run.id]);

  // Steps change less often than events: poll them while the run goes, and
  // once more when it ends so the timeline shows the final statuses.
  useEffect(() => {
    let alive = true;
    const load = () => fetch(`/api/runs/${run.id}/steps`)
      .then((r) => (r.ok ? r.json() : []))
      .then((d: Step[]) => { if (alive) setSteps(d); })
      .catch(() => {});
    load();
    if (end) return () => { alive = false; };
    const t = setInterval(load, 3000);
    const tick = setInterval(() => setNow(new Date()), 1000);
    return () => { alive = false; clearInterval(t); clearInterval(tick); };
  }, [run.id, end]);

  const cost = steps.reduce((c, s) => c + s.cost_usd, 0);
  // A run that ended without reaching a step will not reach it: only a run in
  // progress shows what is still to come.
  const rest = end ? [] : upcoming(plan, steps);
  return (
    <section className="live">
      <h2>
        {showIssue && <><a href={`#/issues/${run.issue_key}`}>{run.issue_key}</a> {run.issue_title} · </>}
        <span className="type">{run.workflow}</span>{" "}
        <StatusChip status={end ?? run.status} />
        <span className="type"> · total {usd(cost)}</span>
        {" · "}<a href={`#/runs/${run.id}`}>full transcript</a>
      </h2>
      {steps.length === 0 && rest.length === 0 ? <p className="status">{end ? "No steps ran." : "Starting: waiting for the first step…"}</p> : (
        <ol className="timeline">
          {[...steps, ...rest].map((s) => (
            <li key={s.id} className={s.status === "not started" ? "upcoming" : `st-${s.status}`}>
              <strong>{stepName(s)}</strong> <StatusChip status={s.status} />
              <span className="type">
                {" "}{duration(s.started_at, s.finished_at, now)}
                {who(s) && ` · ${who(s)}`}
                {s.kind === "agent" && s.status !== "not started" && ` · ${usd(s.cost_usd)}`}
              </span>
            </li>
          ))}
        </ol>
      )}
      {latest.length > 0 && (
        <div className="latest">
          {latest.map((e) => (
            <div className={`ev ev-${e.type}`} key={e.seq}>
              <span className="type" title={new Date(e.time).toLocaleString()}>{label(e)}</span>
              <pre>{clip(summary(e))}</pre>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
