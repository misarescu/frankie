import { HistoryIcon } from "lucide-react";
import { AppLink } from "@/components/app/AppLink";
import { StatusBadge } from "@/components/app/StatusBadge";
import { EmptyState } from "@/components/app/states";
import { Card } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { shortID, type IssueDetail } from "@/issues";
import { duration } from "@/live";
import { paths } from "@/paths";
import { usd } from "@/steps";

// IssueRuns is the Runs tab: the issue's own runs, newest first, each
// linking to its run page.
export function IssueRuns({ issue }: { issue: IssueDetail }) {
  if (issue.runs.length === 0) {
    return (
      <EmptyState icon={<HistoryIcon className="size-6" />} title="No runs yet">
        {issue.children.length > 0
          ? "A parent's work runs on the issues under it; its own runs are refinements of its spec."
          : "A run starts once someone starts the issue and the scheduler picks it up."}
      </EmptyState>
    );
  }
  const runs = [...issue.runs].sort((a, b) => b.created_at.localeCompare(a.created_at));
  return (
    <Card className="overflow-hidden">
      <ul aria-label="Runs" className="divide-y divide-line sm:hidden">
        {runs.map((r) => (
          <li key={r.id} className="flex items-start justify-between gap-3 px-4 py-2.5 text-[13px]">
            <div className="min-w-0 space-y-1">
              <AppLink href={paths.run(r.id)} className="mono text-primary hover:underline" title={r.id}>{shortID(r.id)}</AppLink>
              <span className="mono ml-2 text-xs text-muted-fg [overflow-wrap:anywhere]">{r.workflow}</span>
              <p className="text-xs text-muted-fg">{new Date(r.started_at ?? r.created_at).toLocaleString()}</p>
            </div>
            <div className="flex shrink-0 flex-col items-end gap-1">
              <StatusBadge status={r.status} />
              <span className="tnum">{usd(r.cost_usd)}</span>
            </div>
          </li>
        ))}
      </ul>
      <div className="max-sm:hidden">
        <Table className="min-w-[620px]">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>Run</TableHead>
              <TableHead>Workflow</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Started</TableHead>
              <TableHead>Duration</TableHead>
              <TableHead className="text-right">Cost</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {runs.map((r) => (
              <TableRow key={r.id}>
                <TableCell><AppLink href={paths.run(r.id)} className="mono text-primary hover:underline" title={r.id}>{shortID(r.id)}</AppLink></TableCell>
                <TableCell className="mono text-xs">{r.workflow}</TableCell>
                <TableCell><StatusBadge status={r.status} /></TableCell>
                <TableCell className="text-xs whitespace-nowrap text-muted-fg">{new Date(r.started_at ?? r.created_at).toLocaleString()}</TableCell>
                <TableCell className="tnum text-xs">{r.finished_at || r.status === "running" ? duration(r.started_at ?? r.created_at, r.finished_at) : "-"}</TableCell>
                <TableCell className="tnum text-right">{usd(r.cost_usd)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </Card>
  );
}
