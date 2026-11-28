import { useQuery } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { SearchXIcon, WorkflowIcon } from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { approvalsQuery, ApiError, issueQuery, projectsQuery, releaseQuery, runPlanQuery, runStepsQuery } from "@/api/queries";
import { useRunEvents } from "@/api/stream";
import { AppLink } from "@/components/app/AppLink";
import { useCrumbs, useIssueCrumbs } from "@/components/app/Breadcrumbs";
import { GateDecision } from "@/components/app/decisions";
import { IssueLink } from "@/components/app/IssueLink";
import { PageHeader } from "@/components/app/PageHeader";
import { useRunSummary } from "@/components/app/project";
import { Intervene } from "@/components/app/run-intervene";
import { StepIcon, StepList, StepNotes, StepPills, stepFacts, useNow } from "@/components/app/run-steps";
import { Transcript } from "@/components/app/run-transcript";
import { EmptyState, ErrorState, LoadingState } from "@/components/app/states";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Card } from "@/components/ui/card";
import { projectTitle, type Crumb } from "@/crumbs";
import type { RunSummary } from "@/events";
import { formatWhen, shortID, shortSHA } from "@/format";
import { duration } from "@/live";
import { cn } from "@/lib/utils";
import { paths } from "@/paths";
import { releaseName } from "@/releases";
import { runningAgentStep, shownStatus, stepName, timeline, totals, usd, type Step } from "@/steps";
import { connection, hasRun, releaseRunCrumbs, runCrumbs, selectedStepID, stepEvents, type Connection } from "@/transcript";

const route = getRouteApi("/runs/$id");

// RunPage is one run (M9): its steps on the left, the selected step's
// transcript on the right, live while the run goes.
export function RunPage() {
  const { id } = route.useParams();
  const steps = useQuery(runStepsQuery(id, false));
  // The run's status comes from the run list; until it does (or the search
  // ends without it) the page does not know whether the run is live.
  const summary = useRunSummary(id);
  if (steps.isPending || (!summary.settled && !steps.error)) return <Crumbs tail={runCrumbs(id, undefined)}><LoadingState rows={6} /></Crumbs>;
  if (steps.error && !steps.data) {
    const missing = steps.error instanceof ApiError && (steps.error.status === 404 || steps.error.status === 400);
    return (
      <Crumbs tail={runCrumbs(id, undefined)}>
        {missing ? (
          <EmptyState level={1} icon={<SearchXIcon className="size-6" />} title="No such run"
            action={<AppLink href="/" className="text-[13px] text-primary hover:underline">Go to the board</AppLink>}>
            <span className="[overflow-wrap:anywhere]">Frankie has no run <span className="mono">{id}</span>. The link may be mistyped, or from another Frankie.</span>
          </EmptyState>
        ) : (
          <ErrorState level={1} error={steps.error} what="the run" onRetry={() => void steps.refetch()} />
        )}
      </Crumbs>
    );
  }
  return <RunView id={id} run={summary.run} />;
}

function Crumbs({ tail, children }: { tail: Crumb[]; children: ReactNode }) {
  useCrumbs(tail);
  return <>{children}</>;
}

function IssueRunCrumbs({ issueKey, title, tail }: { issueKey: string; title?: string; tail: Crumb[] }) {
  useIssueCrumbs(issueKey, { title, tail });
  return null;
}

function ReleaseRunCrumbs({ releaseID, name, tail }: { releaseID: string; name: string; tail: Crumb[] }) {
  const release = useQuery(releaseQuery(releaseID)).data;
  const project = (useQuery(projectsQuery()).data ?? []).find((p) => p.name === release?.project);
  useCrumbs([...releaseRunCrumbs(project ? { title: projectTitle(project), prefix: project.key_prefix } : undefined, { id: releaseID, name }), ...tail]);
  return null;
}

function PlainCrumbs({ tail }: { tail: Crumb[] }) {
  useCrumbs(tail);
  return null;
}

const connText: Record<Connection, string> = { connecting: "Connecting", live: "Live", reconnecting: "Reconnecting", finished: "Finished" };

function ConnectionChip({ state }: { state: Connection }) {
  return (
    <span role="status" className={cn("flex items-center gap-1.5 text-xs font-medium", state === "live" ? "text-primary" : state === "reconnecting" ? "text-warn" : "text-muted-fg")}>
      <span aria-hidden="true" className={cn("size-2 rounded-full bg-current", state !== "finished" && "pulse-dot")} />
      {connText[state]}
    </span>
  );
}

function RunView({ id, run }: { id: string; run: RunSummary | undefined }) {
  const { step: requested } = route.useSearch();
  const stream = useRunEvents(id);
  const conn = connection(run?.Status, stream);
  const live = conn === "live" || conn === "reconnecting";
  const stepsQ = useQuery(runStepsQuery(id, live));
  const plan = useQuery(runPlanQuery(id)).data;
  const steps = useMemo(() => stepsQ.data ?? [], [stepsQ.data]);
  const tl = useMemo(() => timeline(plan ?? [], steps, !live), [plan, steps, live]);
  const runEvents = stream.events.filter((e) => !e.step_run_id).length;
  const selID = selectedStepID(tl, requested, runEvents > 0);
  const index = tl.findIndex((s) => s.id === selID);
  const sel = index >= 0 ? tl[index] : undefined;
  const events = useMemo(() => (selID ? stepEvents(stream.events, selID) : []), [stream.events, selID]);
  const running = live ? runningAgentStep(steps) : undefined;
  const approval = (useQuery(approvalsQuery()).data ?? []).find((a) => sel && a.step_run_id === sel.id);
  const issue = useQuery({ ...issueQuery(run?.IssueKey ?? ""), enabled: !!run?.IssueKey }).data;
  const workflow = issue?.runs.find((r) => r.id === id)?.workflow;
  const now = useNow(live);
  const t = totals(steps);

  const tail = runCrumbs(id, selID ? { id: selID, name: sel ? stepName(sel) : "Run" } : undefined);
  const status = run?.Status ?? stream.end ?? undefined;
  const started = run?.StartedAt ?? run?.CreatedAt;
  const what = run?.ReleaseID
    ? <>the release agent for <AppLink href={paths.release(run.ReleaseID)} className="text-primary hover:underline">release {releaseName({ version: run.ReleaseVersion, sha: run.ReleaseSHA ?? "" })}</AppLink></>
    : run?.IssueKey ? <>{workflow ?? "run"} for <IssueLink issueKey={run.IssueKey} title={run.IssueTitle} /></> : null;

  return (
    <div className="space-y-5">
      {run?.ReleaseID ? <ReleaseRunCrumbs releaseID={run.ReleaseID} name={releaseName({ version: run.ReleaseVersion, sha: run.ReleaseSHA ?? "" })} tail={tail} />
        : run?.IssueKey ? <IssueRunCrumbs issueKey={run.IssueKey} title={run.IssueTitle} tail={tail} /> : <PlainCrumbs tail={tail} />}
      <PageHeader
        title={<>Run <span className="mono">{shortID(id)}</span>{status && <StatusBadge status={status} className="ml-1 tracking-normal" />}</>}
        sub={
          <span className="[overflow-wrap:anywhere]">
            {what}{what && started && " · "}
            {started && <>started {formatWhen(started)} · {duration(started, run?.FinishedAt ?? undefined, now)}</>}
          </span>
        }
        actions={
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px]">
            <ConnectionChip state={conn} />
            <span className="tnum"><span className="text-muted-fg">Cost </span><span className="font-medium">{usd(t.cost)}</span></span>
            <span className="tnum"><span className="text-muted-fg">Tokens </span>{t.input.toLocaleString()} in · {t.output.toLocaleString()} out</span>
          </div>
        }
      />
      {tl.length === 0 && runEvents === 0 ? (
        <EmptyState icon={<WorkflowIcon className="size-6" />} title={live ? "No steps yet" : "This run has no steps"}>
          {live ? "The run's first step shows here once it starts, with its transcript beside it." : "The run ended before any step started."}
        </EmptyState>
      ) : (
        <div className="grid gap-4 lg:grid-cols-[300px_minmax(0,1fr)] lg:gap-5">
          <StepList runID={id} timeline={tl} selected={selID} runEvents={runEvents} />
          <div className="min-w-0 space-y-3">
            <StepPills runID={id} timeline={tl} selected={selID} runEvents={runEvents} />
            <Card className="flex min-w-0 flex-col overflow-hidden">
              <SelectedHead step={sel} timeline={tl} index={index} now={now} />
              <Transcript events={events} follow={sel?.status === "running"} empty={emptyText(sel, live)} />
              {approval && (
                <div className="border-t border-line bg-muted px-4 py-3">
                  <p className="mb-2 text-xs font-medium text-muted-fg [overflow-wrap:anywhere]">This gate waits for a decision: {approval.reason}</p>
                  <GateDecision approval={approval} />
                </div>
              )}
              {running && sel?.id === running.id && <Intervene key={running.id} step={running} />}
              {running && sel?.id !== running.id && (
                <p className="border-t border-line bg-muted px-4 py-2.5 text-xs text-muted-fg">
                  <span className="mono">{stepName(running)}</span> is running.{" "}
                  <AppLink href={paths.run(id, running.id)} className="text-primary hover:underline">Open it to steer or abort it</AppLink>
                </p>
              )}
            </Card>
          </div>
        </div>
      )}
    </div>
  );
}

function emptyText(s: Step | undefined, live: boolean): string {
  if (!s) return "The run's own events show here.";
  if (s.status === "skipped") return `Skipped: it ${s.skipped_because ?? "did not meet its condition"}.`;
  if (!hasRun(s)) return "This step has not run yet. It starts when the run reaches it.";
  if (s.status === "running" || s.status === "pending") return live ? "No events from this step yet; they show here as they happen." : "This step recorded no events.";
  return "This step recorded no events.";
}

function SelectedHead({ step, timeline, index, now }: { step?: Step; timeline: Step[]; index: number; now: Date }) {
  if (!step) {
    return (
      <div className="border-b border-line px-4 py-2.5">
        <h2 className="text-[13px] font-semibold">Run</h2>
        <p className="text-xs text-muted-fg">events of the run and its issue, not of a step</p>
      </div>
    );
  }
  const facts = stepFacts(step, now);
  const tokens = step.input_tokens + step.output_tokens > 0 ? `${step.input_tokens.toLocaleString()} in · ${step.output_tokens.toLocaleString()} out` : "";
  return (
    <div className="flex flex-wrap items-start justify-between gap-2 border-b border-line px-4 py-2.5">
      <div className="flex min-w-0 flex-1 items-start gap-2">
        <StepIcon step={step} className="mt-0.5" />
        <div className="min-w-0">
          <h2 className="mono text-[13px] font-semibold [overflow-wrap:anywhere]">{stepName(step)}</h2>
          <p className="text-xs text-muted-fg [overflow-wrap:anywhere]">
            {[facts || step.kind, tokens, step.commit_sha ? `at ${shortSHA(step.commit_sha)}` : ""].filter(Boolean).join(" · ")}
          </p>
          <StepNotes timeline={timeline} index={index} />
        </div>
      </div>
      <StatusBadge status={shownStatus(step)} />
    </div>
  );
}

