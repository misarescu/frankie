import { useQuery } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { AlertTriangleIcon, HistoryIcon, PackageIcon } from "lucide-react";
import { releasesQuery, runsQuery } from "@/api/queries";
import { AppLink } from "@/components/app/AppLink";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { IssueLink } from "@/components/app/IssueLink";
import { PageHeader } from "@/components/app/PageHeader";
import { useProject } from "@/components/app/project";
import { StatusBadge } from "@/components/app/StatusBadge";
import { EmptyState, ErrorState, LoadingState } from "@/components/app/states";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Segmented } from "@/components/ui/segmented";
import { projectCrumb } from "@/crumbs";
import type { RunSummary } from "@/events";
import { shortID } from "@/issues";
import { paths, type RunsFilter } from "@/paths";
import { releaseName } from "@/releases";
import { ago, filterRuns, projectRuns } from "@/runs";
import { usd } from "@/steps";
import { cn } from "@/lib/utils";

const route = getRouteApi("/projects/$project/runs");

const filters: { value: RunsFilter | "all"; label: string }[] = [
  { value: "all", label: "All" },
  { value: "active", label: "Active" },
  { value: "failed", label: "Failed" },
];

const th = "px-2 py-2 font-medium whitespace-nowrap";
const cell = "px-2 py-2 align-middle max-md:p-0";

// RunsPage is a project's runs (M9): its issues' runs and the release
// agent's runs of its releases, newest first, filtered by all, active or
// failed.
export function RunsPage() {
  const project = useProject();
  const { status } = route.useSearch();
  const navigate = route.useNavigate();
  useCrumbs([projectCrumb(project), { label: "Runs" }]);
  const runs = useQuery(runsQuery());
  const releases = useQuery(releasesQuery(project.name));

  const header = (
    <PageHeader
      title="Runs"
      sub="every workflow run of the project, newest first"
      actions={
        <Segmented
          label="Show runs"
          value={status ?? "all"}
          onValueChange={(v) => void navigate({ search: { status: v === "all" ? undefined : v } })}
          options={filters}
        />
      }
    />
  );
  if (runs.isPending || releases.isPending) return <div className="space-y-5">{header}<LoadingState rows={8} /></div>;
  if (!runs.data) return <div className="space-y-5">{header}<ErrorState error={runs.error} what="the runs" onRetry={() => void runs.refetch()} /></div>;

  const releaseIDs = new Set(releases.data?.map((r) => r.id));
  const mine = projectRuns(runs.data, project.key_prefix, releaseIDs);
  const shown = filterRuns(mine, status);
  const filterName = filters.find((f) => f.value === status)?.label.toLowerCase();

  return (
    <div className="space-y-5">
      {header}
      {(runs.isError || releases.isError) && (
        <Alert tone="warn">
          <AlertTriangleIcon className="size-4" />
          <div className="min-w-0 [overflow-wrap:anywhere]">
            {runs.isError
              ? `Could not refresh the runs (${runs.error.message}); showing what was loaded last.`
              : `Could not load the releases (${releases.error?.message}), so the release agent's runs are left out.`}
          </div>
        </Alert>
      )}
      {mine.length === 0 ? (
        <EmptyState icon={<HistoryIcon className="size-6" />} title="No runs yet">
          A run appears here once an issue of {project.name} starts (from the board or its page), or a release of it is written up by the release agent.
        </EmptyState>
      ) : shown.length === 0 ? (
        <EmptyState
          icon={<HistoryIcon className="size-6" />}
          title={`No ${filterName} runs`}
          action={<Button variant="outline" size="sm" onClick={() => void navigate({ search: {} })}>Show all runs</Button>}
        >
          {status === "active" ? "Nothing of this project is running right now." : "No run of this project failed."}
        </EmptyState>
      ) : (
        <RunsTable runs={shown} />
      )}
    </div>
  );
}

function RunsTable({ runs }: { runs: RunSummary[] }) {
  const now = new Date();
  return (
    <Card className="overflow-hidden">
      <table className="w-full text-[13px] max-md:block">
        <thead className="border-b border-line text-left text-xs2 tracking-wider text-muted-fg uppercase max-md:hidden">
          <tr>
            <th className={cn(th, "pl-4")}>Run</th>
            <th className={th}>For</th>
            <th className={th}>Status</th>
            <th className={th}>Started</th>
            <th className={cn(th, "pr-4 text-right")}>Cost</th>
          </tr>
        </thead>
        <tbody className="max-md:block">
          {runs.map((r) => (
            <tr key={r.ID} className="border-b border-line last:border-0 hover:bg-hover max-md:flex max-md:flex-wrap max-md:items-center max-md:gap-x-3 max-md:gap-y-1.5 max-md:px-3 max-md:py-2.5">
              <td className={cn(cell, "pl-4 max-md:order-1")}>
                <AppLink href={paths.run(r.ID)} className="mono text-xs font-medium text-primary hover:underline" title={`run ${r.ID}`}>
                  <span className="sr-only">run </span>{shortID(r.ID)}
                </AppLink>
              </td>
              <td className={cn(cell, "min-w-0 max-md:order-0 max-md:basis-full")}>
                {r.ReleaseID ? (
                  <span className="flex min-w-0 items-center gap-1.5">
                    <PackageIcon className="size-3.5 shrink-0 text-muted-fg" />
                    <AppLink href={paths.release(r.ReleaseID)} className="text-primary hover:underline">
                      release <span className="mono text-[12.5px]">{releaseName({ sha: r.ReleaseSHA ?? "", version: r.ReleaseVersion })}</span>
                    </AppLink>
                    <span className="text-muted-fg">· the release agent</span>
                  </span>
                ) : (
                  <IssueLink issueKey={r.IssueKey} title={r.IssueTitle} className="line-clamp-2 [overflow-wrap:anywhere]" />
                )}
              </td>
              <td className={cn(cell, "max-md:order-2")}><StatusBadge status={r.Status} /></td>
              <td className={cn(cell, "tnum whitespace-nowrap text-muted-fg max-md:order-3")} title={new Date(r.CreatedAt).toLocaleString()}>
                {ago(r.CreatedAt, now)}
              </td>
              <td className={cn(cell, "tnum pr-4 text-right max-md:order-4 max-md:ml-auto max-md:pr-0")}>{usd(r.CostUSD ?? 0)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  );
}
