import { useQuery } from "@tanstack/react-query";
import { ArrowRightIcon, BotIcon, ListOrderedIcon, ShieldCheckIcon } from "lucide-react";
import { Fragment, useEffect, useState } from "react";
import { runPlanQuery, runStepsQuery } from "@/api/queries";
import { useRunEvents } from "@/api/stream";
import { AppLink } from "@/components/app/AppLink";
import { IssueLink } from "@/components/app/IssueLink";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { label, summary, type RunEvent } from "@/events";
import { shortID } from "@/issues";
import { cn } from "@/lib/utils";
import { clip, duration, keepLatest, LATEST, type LiveRunRef } from "@/live";
import { paths } from "@/paths";
import { isActive, statusTone } from "@/status";
import { shownStatus, stepName, timeline, totals, usd, waitNote, who, type Step } from "@/steps";

const ring: Record<string, string> = { ok: "border-ok", bad: "border-bad", primary: "border-primary", warn: "border-warn" };

// StepPill is one step of a run on the issue page: its kind, name and attempt,
// in its status's tone; a step that ran links to it on the run page.
function StepPill({ step, runID, now }: { step: Step; runID: string; now: Date }) {
  const status = shownStatus(step);
  const unrun = step.status === "not started" || step.status === "skipped";
  const Icon = step.kind === "agent" ? BotIcon : step.kind === "gate" ? ShieldCheckIcon : ListOrderedIcon;
  const title = [stepName(step), status.replaceAll("_", " "), who(step), duration(step.started_at, step.finished_at, now), step.skipped_because]
    .filter(Boolean).join(" · ");
  const body = (
    <>
      <Icon className="size-3.5 text-muted-fg" aria-hidden="true" />
      <span className="mono">{step.step_id}{step.attempt > 1 ? ` #${step.attempt}` : ""}</span>
      {isActive(status) && <span aria-hidden="true" className="pulse-dot size-1.5 rounded-full bg-primary" />}
      <span className="sr-only">{status.replaceAll("_", " ")}</span>
    </>
  );
  const cls = cn("relative flex shrink-0 items-center gap-1.5 rounded-md border bg-card px-2 py-1 text-xs", ring[statusTone(status)] ?? "border-line",
    step.status === "skipped" && "border-dashed opacity-60", (step.status === "not started" || step.status === "pending") && "opacity-60");
  if (unrun) return <span className={cls} title={title}>{body}</span>;
  return <AppLink href={paths.run(runID, step.id)} className={cn(cls, "hover:bg-hover")} title={title}>{body}</AppLink>;
}

// latestActivity is what the agent said and did last, from the run's stream.
const latestActivity = (events: RunEvent[]): RunEvent[] => events.reduce<RunEvent[]>((l, e) => keepLatest(l, e, LATEST), []);

// LiveRunCard follows one run on the issue page (M5.1): its steps as pills
// linking to the run page, and the agent's latest messages and tool calls
// from the run's shared stream. The full transcript, steer and abort are on
// the run page.
export function LiveRunCard({ run, showIssue }: { run: LiveRunRef; showIssue: boolean }) {
  const { events, end, reconnecting } = useRunEvents(run.id);
  const ended = end !== null || !isActive(run.status);
  const steps = useQuery(runStepsQuery(run.id, !ended));
  const plan = useQuery(runPlanQuery(run.id));
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    if (ended) return;
    const t = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(t);
  }, [ended]);

  const ran = steps.data ?? [];
  const shown = timeline(plan.data ?? [], ran, ended);
  const status = end ?? run.status;
  const latest = latestActivity(events);
  const current = ran.filter((s) => s.status === "running").at(-1);
  const wait = current && waitNote(current, paths.run(run.id, current.id));
  const first = ran.find((s) => s.started_at)?.started_at ?? run.created_at;
  const last = ended ? ran.map((s) => s.finished_at).filter(Boolean).sort().at(-1) : undefined;
  return (
    <Card className="overflow-hidden" aria-label={`Run ${shortID(run.id)}`}>
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-4 py-2.5">
        <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[13px]">
          {!ended && <span aria-hidden="true" className="pulse-dot size-2 shrink-0 rounded-full bg-primary" />}
          <span className="font-semibold">Run <span className="mono">{shortID(run.id)}</span> {ended ? "" : "is live"}</span>
          {ended && <StatusBadge status={status} />}
          {showIssue && <IssueLink issueKey={run.issue_key} title={run.issue_title} className="[overflow-wrap:anywhere]" />}
          <span className="text-muted-fg">
            · {run.workflow} · <span className="tnum">{duration(first, last, now)}</span> · <span className="tnum">{usd(totals(ran).cost)}</span>
          </span>
        </div>
        <Button asChild variant="outline" size="sm">
          <AppLink href={paths.run(run.id)}>Open run<ArrowRightIcon className="size-3.5" /></AppLink>
        </Button>
      </div>
      <div className="scroll-thin flex items-center gap-1.5 overflow-x-auto px-4 py-3">
        {shown.length === 0 && (
          <span className="text-[13px] text-muted-fg">
            {steps.isError ? `Could not load its steps: ${steps.error.message}` : ended ? "No steps ran." : "Starting: waiting for the first step."}
          </span>
        )}
        {shown.map((s, n) => (
          <Fragment key={s.id}>
            {n > 0 && <span aria-hidden="true" className="h-px w-3 shrink-0 bg-line" />}
            <StepPill step={s} runID={run.id} now={now} />
          </Fragment>
        ))}
      </div>
      {(current || wait) && (
        <div className="border-t border-line px-4 py-2 text-xs text-muted-fg">
          {current && <span><span className="mono">{stepName(current)}</span>{who(current) && ` · ${who(current)}`} · {duration(current.started_at, undefined, now)}</span>}
          {wait && (
            <span>
              {" · "}
              {wait.href ? <AppLink href={wait.href} className="text-primary hover:underline">{wait.text}</AppLink> : wait.text}
            </span>
          )}
        </div>
      )}
      {latest.length > 0 && (
        <ol aria-label="Latest activity" className="divide-y divide-line border-t border-line bg-code">
          {latest.map((e) => (
            <li key={e.seq} className="px-4 py-2 text-[13px]">
              <span className={cn("text-xs font-medium text-muted-fg", e.type === "error" && "text-bad", e.type === "question" && "text-warn")}
                title={new Date(e.time).toLocaleString()}>{label(e)}</span>
              <p className={cn("mt-0.5 line-clamp-3 whitespace-pre-wrap [overflow-wrap:anywhere]", e.type === "tool_call" && "mono text-xs")}>{clip(summary(e))}</p>
            </li>
          ))}
        </ol>
      )}
      {reconnecting && <p className="border-t border-line px-4 py-2 text-xs text-warn">The live stream dropped; reconnecting.</p>}
    </Card>
  );
}
