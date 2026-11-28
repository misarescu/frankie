import { useQuery } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { ExternalLinkIcon, PackageXIcon, RocketIcon, ShieldCheckIcon, TriangleAlertIcon } from "lucide-react";
import type { ReactNode } from "react";
import { ApiError, approvalsQuery, issuesQuery, projectsQuery, releaseQuery } from "@/api/queries";
import { AppLink } from "@/components/app/AppLink";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { GateDecision } from "@/components/app/decisions";
import { IssueLink } from "@/components/app/IssueLink";
import { PageHeader } from "@/components/app/PageHeader";
import { useCurrentProject } from "@/components/app/project";
import { Pipeline } from "@/components/app/releases-pipeline";
import { EmptyState, ErrorState, LoadingState } from "@/components/app/states";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import type { Approval } from "@/approvals";
import { expiresIn } from "@/approvals";
import { projectCrumb, type Crumb } from "@/crumbs";
import type { Project } from "@/issues";
import { paths } from "@/paths";
import { formatWhen, shortSHA } from "@/format";
import {
  isLive, notesSourceText, pipelineStages, releaseName, verificationState, verificationText, versionText,
  type Deployment, type ReleaseDetail,
} from "@/releases";
import { usd } from "@/steps";

const route = getRouteApi("/releases/$id");

const releasesCrumbs = (p: Project | undefined, last: string): Crumb[] =>
  [...(p ? [projectCrumb(p), { label: "Releases", href: paths.releases(p.key_prefix) }] : []), { label: last }];

// ReleasePage is one release (D130): its version and notes (D129), each
// stage's deployment with its verification and the failed tests, the gate
// before production decided in place while it waits (D139), its issues, the
// bug issue a failed verification opened and the release agent's runs.
export function ReleasePage() {
  const { id } = route.useParams();
  const rel = useQuery(releaseQuery(id));
  const projects = useQuery(projectsQuery()).data ?? [];
  const current = useCurrentProject().project;
  const project = rel.data ? projects.find((p) => p.name === rel.data.project) : current;
  useCrumbs(releasesCrumbs(project, rel.data ? releaseName(rel.data) : "Release"));

  if (rel.isPending) return <LoadingState rows={5} />;
  if (rel.isError && !rel.data) {
    if (rel.error instanceof ApiError && rel.error.status === 404) {
      return (
        <EmptyState level={1} icon={<PackageXIcon className="size-6" />} title="No such release"
          action={project && <Button asChild variant="outline" size="sm"><AppLink href={paths.releases(project.key_prefix)}>Open the releases of {project.name}</AppLink></Button>}>
          No release has the id <span className="mono [overflow-wrap:anywhere]">{id}</span>.
        </EmptyState>
      );
    }
    return <ErrorState level={1} error={rel.error} what="the release" onRetry={() => void rel.refetch()} />;
  }
  return <Release rel={rel.data} project={project} stale={rel.isError ? rel.error.message : undefined} />;
}

function Release({ rel, project, stale }: { rel: ReleaseDetail; project: Project | undefined; stale?: string }) {
  const runIDs = new Set(rel.runs.map((r) => r.id));
  const gates = (useQuery(approvalsQuery()).data ?? []).filter((a) => runIDs.has(a.run_id));
  const issues = useQuery({ ...issuesQuery(project?.name ?? ""), enabled: !!project && !!rel.bug_issue_ref }).data ?? [];
  const bugIssue = rel.bug_issue_ref ? issues.find((i) => i.source_ref === rel.bug_issue_ref) : undefined;
  const stages = pipelineStages(rel.stages, gates.map((g) => g.step));

  return (
    <div className="space-y-5">
      <PageHeader
        icon={<RocketIcon className="size-5 shrink-0 text-muted-fg" />}
        title={rel.version ? rel.version : <span className="mono">{releaseName(rel)}</span>}
        sub={
          <span className="[overflow-wrap:anywhere]">
            {rel.repo} at <span className="mono" title={rel.sha}>{shortSHA(rel.sha)}</span>
            {rel.bump && <> · {rel.bump} bump{rel.notes_source === "agent" ? " proposed by the release agent" : ""}</>}
            {" · "}created {formatWhen(rel.created_at)}
          </span>
        }
        actions={<StatusBadge status={rel.status} />}
      />
      {stale && <Alert tone="warn">Could not refresh the release: {stale}. Showing what loaded last.</Alert>}
      {gates.map((g) => <GateBanner key={g.step_run_id} gate={g} />)}
      {(rel.reason || rel.bug_issue_ref) && (
        <Alert tone={rel.status === "succeeded" ? "warn" : "bad"}>
          <TriangleAlertIcon className="size-4" />
          <div className="min-w-0 space-y-1 [overflow-wrap:anywhere]">
            {rel.reason && <p>{rel.reason}</p>}
            {rel.bug_issue_ref && (
              <p className="flex flex-wrap items-baseline gap-x-1.5">
                Bug issue{bugIssue ? <IssueLink issueKey={bugIssue.key} title={bugIssue.title} /> : null}
                <a href={rel.bug_issue_ref} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">
                  {bugIssue ? "on the provider" : rel.bug_issue_ref}<ExternalLinkIcon className="size-3" />
                </a>
              </p>
            )}
          </div>
        </Alert>
      )}
      <Card className="px-4 py-3"><Pipeline stages={stages} big /></Card>

      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div className="min-w-0 space-y-5">
          <section aria-label="Stages" className="space-y-3">
            <h2 className="text-[13px] font-semibold">Stages</h2>
            {rel.deployments.length === 0 && (
              <Card className="px-4 py-3 text-[13px] text-muted-fg">
                {gates.length > 0 ? "No stage deployed yet; the first one waits for you." : isLive(rel.status) ? "No stage deployed yet." : "No stage was deployed."}
              </Card>
            )}
            {rel.deployments.map((d) => <DeploymentCard key={d.stage} d={d} />)}
          </section>
          <Card>
            <CardHeader>
              <div className="min-w-0">
                <CardTitle>Release notes</CardTitle>
                {rel.notes && notesSourceText(rel.notes_source) && <CardDescription>{notesSourceText(rel.notes_source)}</CardDescription>}
              </div>
            </CardHeader>
            <CardContent>
              {rel.notes
                ? <div className="text-[13px] leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]">{rel.notes}</div>
                : <p className="text-[13px] text-muted-fg">{isLive(rel.status) ? "Not written yet." : "None."}</p>}
            </CardContent>
          </Card>
        </div>

        <aside className="min-w-0 space-y-5">
          <Card>
            <CardHeader><CardTitle>Details</CardTitle></CardHeader>
            <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 px-4 py-3 text-[13px]">
              <Row label="Version">{versionText(rel)}</Row>
              {rel.provider_release_url && (
                <Row label="Published">
                  <a href={rel.provider_release_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">
                    provider release<ExternalLinkIcon className="size-3" />
                  </a>
                </Row>
              )}
              <Row label="Commit"><span className="mono text-xs [overflow-wrap:anywhere]">{rel.sha}</span></Row>
              <Row label="Project">{project ? <AppLink href={paths.board(project.key_prefix)} className="text-primary hover:underline">{rel.project}</AppLink> : rel.project}</Row>
              <Row label="Created"><span className="tnum">{formatWhen(rel.created_at)}</span></Row>
              <Row label="Finished"><span className="tnum">{rel.finished_at ? formatWhen(rel.finished_at) : "-"}</span></Row>
            </dl>
          </Card>
          <Card>
            <CardHeader><CardTitle>Issues in this release</CardTitle></CardHeader>
            <ul className="space-y-1.5 px-4 py-3 text-[13px]">
              {rel.issue_details.length === 0 && <li className="text-muted-fg">None merged by Frankie.</li>}
              {rel.issue_details.map((i) => (
                <li key={i.key} className="flex items-start justify-between gap-2">
                  <IssueLink issueKey={i.key} title={i.title} className="[overflow-wrap:anywhere]" />
                  <StatusBadge status={i.status} className="shrink-0" />
                </li>
              ))}
            </ul>
          </Card>
          {rel.runs.length > 0 && (
            <Card>
              <CardHeader>
                <CardTitle>Release agent</CardTitle>
                <span className="tnum text-xs text-muted-fg" title="the release agent's cost">{usd(rel.cost_usd)}</span>
              </CardHeader>
              <ul className="divide-y divide-line text-[13px]">
                {rel.runs.map((r) => (
                  <li key={r.id} className="space-y-1 px-4 py-2.5">
                    <div className="flex items-center justify-between gap-2">
                      <AppLink href={paths.run(r.id)} className="tnum text-primary hover:underline">Run of {formatWhen(r.created_at)}</AppLink>
                      <StatusBadge status={r.status} />
                    </div>
                    <div className="flex items-baseline justify-between gap-2 text-xs text-muted-fg">
                      <span className="mono [overflow-wrap:anywhere]">{r.steps.length ? r.steps.join(", ") : "no steps yet"}</span>
                      <span className="tnum shrink-0">{usd(r.cost_usd)}</span>
                    </div>
                  </li>
                ))}
              </ul>
            </Card>
          )}
        </aside>
      </div>
    </div>
  );
}

function Row({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-fg">{label}</dt>
      <dd className="min-w-0 [overflow-wrap:anywhere]">{children}</dd>
    </>
  );
}

// GateBanner is the human gate before a stage (D139), decided in place.
function GateBanner({ gate }: { gate: Approval }) {
  const expiry = expiresIn(gate.expires_at);
  return (
    <section aria-label={`${gate.step} gate`} className="rounded-lg border border-warn/40 bg-warn-soft">
      <div className="flex items-start gap-3 px-4 py-3 text-[13px]">
        <ShieldCheckIcon className="mt-0.5 size-4 shrink-0 text-warn" />
        <div className="min-w-0">
          <h2 className="font-semibold">{gate.step.charAt(0).toUpperCase() + gate.step.slice(1)} waits for you</h2>
          <p className="text-muted-fg [overflow-wrap:anywhere]">{gate.reason}</p>
          <p className="mt-1 text-xs text-muted-fg">
            {expiry === "expired" ? "The gate has expired." : `The gate expires in ${expiry}.`}
          </p>
        </div>
      </div>
      <div className="border-t border-warn/30 px-4 py-3"><GateDecision approval={gate} /></div>
    </section>
  );
}

// DeploymentCard is one stage: its driver and outcome, why it ended as it
// did, and how its verification ended with the failed tests and the issues
// they are tagged with.
function DeploymentCard({ d }: { d: Deployment }) {
  const v = d.verification;
  return (
    <Card>
      <CardHeader>
        <div className="min-w-0">
          <CardTitle className="flex flex-wrap items-baseline gap-x-2">
            <span className="mono [overflow-wrap:anywhere]">{d.stage}</span>
            <span className="text-xs font-normal text-muted-fg">{d.driver}</span>
          </CardTitle>
          <CardDescription className="tnum">
            {d.started_at ? `started ${formatWhen(d.started_at)}` : "not started"}
            {d.finished_at && `, finished ${formatWhen(d.finished_at)}`}
          </CardDescription>
        </div>
        <StatusBadge status={d.status} />
      </CardHeader>
      {(d.reason || v) && (
        <CardContent className="space-y-2 text-[13px]">
          {d.reason && <p className="[overflow-wrap:anywhere]">{d.reason}</p>}
          {v && (
            <div className="space-y-2">
              <p className="flex flex-wrap items-center gap-2">
                <StatusBadge status={verificationState(v)} label={verificationState(v) === "skipped" ? "no verification" : `verification ${verificationState(v)}`} />
                <span className="tnum font-medium [overflow-wrap:anywhere]">{verificationText(v)}</span>
              </p>
              {v.failures && v.failures.length > 0 && (
                <ul className="divide-y divide-line rounded-md border border-line" aria-label="Failed tests">
                  {v.failures.map((f, n) => (
                    <li key={n} className="space-y-1 px-3 py-2">
                      <div className="flex flex-wrap items-baseline gap-x-2">
                        <span className="mono text-xs text-bad [overflow-wrap:anywhere]">{f.test}</span>
                        {f.issueKeys?.map((k) => <IssueLink key={k} issueKey={k} />)}
                      </div>
                      {f.message && <pre className="mono rounded bg-code px-2 py-1 text-xs whitespace-pre-wrap [overflow-wrap:anywhere]">{f.message}</pre>}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
        </CardContent>
      )}
    </Card>
  );
}
