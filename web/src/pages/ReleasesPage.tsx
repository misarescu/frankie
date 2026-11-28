import { useQuery } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { RocketIcon } from "lucide-react";
import type { MouseEvent } from "react";
import { approvalsQuery, releasesQuery, runsQuery } from "@/api/queries";
import { AppLink } from "@/components/app/AppLink";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { IssueLink } from "@/components/app/IssueLink";
import { PageHeader } from "@/components/app/PageHeader";
import { useProject } from "@/components/app/project";
import { Pipeline } from "@/components/app/releases-pipeline";
import { EmptyState, ErrorState, LoadingState } from "@/components/app/states";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { projectCrumb } from "@/crumbs";
import { cn } from "@/lib/utils";
import { releaseOf } from "@/inbox";
import { paths } from "@/paths";
import { formatWhen, pipelineStages, releaseName, shortSHA, type ReleaseSummary, type StageStatus } from "@/releases";

// ReleasesPage lists a project's releases newest first (D130): each one's
// version or commit, status, its stages as a pipeline (a stage waiting at its
// human gate included, D139), its issues and when it started and ended.
export function ReleasesPage() {
  const project = useProject();
  useCrumbs([projectCrumb(project), { label: "Releases" }]);
  const releases = useQuery(releasesQuery(project.name));
  const approvals = useQuery(approvalsQuery()).data ?? [];
  const runs = useQuery(runsQuery()).data ?? [];

  const header = <PageHeader title="Releases" sub="Every merge from the merge queue asks for a release; its stages come from .factory/config.yaml." />;
  if (releases.isPending) return <div className="space-y-5">{header}<LoadingState rows={4} /></div>;
  if (releases.isError && !releases.data) {
    return <div className="space-y-5">{header}<ErrorState error={releases.error} what="the releases" onRetry={() => void releases.refetch()} /></div>;
  }
  const list = releases.data;
  const stagesOf = (r: ReleaseSummary): StageStatus[] =>
    pipelineStages(r.stages, approvals.filter((a) => releaseOf(a, runs, list) === r.id).map((a) => a.step));

  return (
    <div className="space-y-5">
      {header}
      {releases.isError && <Alert tone="warn">Could not refresh the releases: {releases.error.message}. Showing what loaded last.</Alert>}
      {list.length === 0 ? (
        <EmptyState icon={<RocketIcon className="size-6" />} title="No releases yet">
          A merge from the merge queue asks for a release when the project declares stages in .factory/config.yaml.
        </EmptyState>
      ) : (
        <>
          <ReleasesTable releases={list} stagesOf={stagesOf} />
          <ul className="space-y-3 md:hidden">
            {list.map((r) => <li key={r.id}><ReleaseCard release={r} stages={stagesOf(r)} /></li>)}
          </ul>
        </>
      )}
    </div>
  );
}

function Name({ r }: { r: ReleaseSummary }) {
  return (
    <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
      <AppLink href={paths.release(r.id)} className={cn("min-w-0 font-semibold text-fg [overflow-wrap:anywhere] hover:underline", !r.version && "mono")}>{releaseName(r)}</AppLink>
      {r.version && <span className="mono text-xs text-muted-fg" title={r.sha}>{shortSHA(r.sha)}</span>}
      {r.bump && <span className="text-xs text-muted-fg">{r.bump}</span>}
    </span>
  );
}

// Issues is how many issues a release contains, with the first few linked.
function Issues({ keys }: { keys: string[] }) {
  if (keys.length === 0) return <span className="text-muted-fg">none</span>;
  const shown = keys.slice(0, 3);
  return (
    <span className="inline-flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
      <Badge tone="neutral" className="tnum" title={`${keys.length} issue${keys.length === 1 ? "" : "s"}`}>{keys.length}</Badge>
      {shown.map((k) => <IssueLink key={k} issueKey={k} />)}
      {keys.length > shown.length && <span className="text-xs text-muted-fg">+{keys.length - shown.length} more</span>}
    </span>
  );
}

const finished = (r: ReleaseSummary) => (r.finished_at ? formatWhen(r.finished_at) : "-");

function ReleasesTable({ releases, stagesOf }: { releases: ReleaseSummary[]; stagesOf: (r: ReleaseSummary) => StageStatus[] }) {
  const router = useRouter();
  // A row opens its release; a link inside it goes where it points.
  const open = (r: ReleaseSummary) => (e: MouseEvent) => {
    if ((e.target as HTMLElement).closest("a")) return;
    void router.navigate({ href: paths.release(r.id) });
  };
  return (
    <Card className="hidden overflow-hidden md:block">
      <Table>
        <TableHeader className="text-xs2 uppercase tracking-wider">
          <TableRow className="hover:bg-transparent">
            <TableHead>Release</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Stages</TableHead>
            <TableHead>Issues</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>Finished</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {releases.map((r) => (
            <TableRow key={r.id} onClick={open(r)} className="cursor-pointer">
              <TableCell><Name r={r} /></TableCell>
              <TableCell><StatusBadge status={r.status} /></TableCell>
              <TableCell className="min-w-64"><Pipeline stages={stagesOf(r)} /></TableCell>
              <TableCell className="whitespace-nowrap"><Issues keys={r.issues} /></TableCell>
              <TableCell className="tnum whitespace-nowrap text-muted-fg">{formatWhen(r.created_at)}</TableCell>
              <TableCell className="tnum whitespace-nowrap text-muted-fg">{finished(r)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}

// ReleaseCard is a release at phone width, where the table does not fit.
function ReleaseCard({ release: r, stages }: { release: ReleaseSummary; stages: StageStatus[] }) {
  return (
    <Card className="space-y-2 px-4 py-3 text-[13px]">
      <div className="flex items-start justify-between gap-2">
        <Name r={r} />
        <StatusBadge status={r.status} />
      </div>
      <Pipeline stages={stages} />
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs">
        <Issues keys={r.issues} />
      </div>
      <p className="tnum text-xs text-muted-fg">
        created {formatWhen(r.created_at)}{r.finished_at && <>, finished {formatWhen(r.finished_at)}</>}
      </p>
    </Card>
  );
}
