import { useQuery } from "@tanstack/react-query";
import { ArrowRightIcon, ListTreeIcon } from "lucide-react";
import { issuesQuery } from "@/api/queries";
import { AppLink } from "@/components/app/AppLink";
import { Cost } from "@/components/app/Cost";
import { IssueLink } from "@/components/app/IssueLink";
import { StatusBadge } from "@/components/app/StatusBadge";
import { EmptyState, ErrorState, LoadingState } from "@/components/app/states";
import { TypeIcon } from "@/components/app/TypeIcon";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { isFinished, leafStatuses, progress, progressText, subtree, type IssueDetail } from "@/issues";
import { paths } from "@/paths";
import { usd } from "@/steps";

// IssueChildren is a parent's Issues tab (D74, D142): every issue under it as
// a tree, each with its status, what it waits for, its change request, latest
// run and total cost, and a total row that is the parent's total.
export function IssueChildren({ issue }: { issue: IssueDetail }) {
  const q = useQuery(issuesQuery(issue.project));
  if (q.isPending) return <LoadingState rows={5} />;
  if (q.isError) return <ErrorState error={q.error} what={`the issues under ${issue.key}`} onRetry={() => void q.refetch()} />;
  const { rows } = subtree(q.data, issue.key);
  if (rows.length === 0) {
    return (
      <EmptyState icon={<ListTreeIcon className="size-6" />} title={`No issues under ${issue.key}`}>
        Its sub-issues appear here once they are synced from GitHub.
      </EmptyState>
    );
  }
  const parents = new Set(q.data.flatMap((i) => (i.parent ? [i.parent] : [])));
  const status = new Map(q.data.map((i) => [i.key, i.status]));
  return (
    <Card className="overflow-hidden">
      <CardHeader className="flex-wrap">
        <CardTitle>Issues under {issue.key}</CardTitle>
        <CardDescription>
          {rows.length} {rows.length === 1 ? "issue" : "issues"} · {progressText(progress(leafStatuses(q.data, issue.key)))} · total {usd(issue.total_cost_usd)}
        </CardDescription>
      </CardHeader>
      <ul aria-label={`Issues under ${issue.key}`} className="divide-y divide-line sm:hidden">
        {rows.map(({ issue: c, depth }) => (
          <li key={c.key} className="flex items-start gap-2 py-2.5 pr-4 text-[13px]" style={{ paddingLeft: 16 + depth * 14 }}>
            <span className="mt-0.5"><TypeIcon type={c.type} /></span>
            <div className="min-w-0 flex-1 space-y-1">
              <IssueLink issueKey={c.key} title={c.title} className="block [overflow-wrap:anywhere]" />
              <StatusBadge status={c.status} />
            </div>
            <Cost own={c.cost_usd} total={c.total_cost_usd} parent={parents.has(c.key)} className="shrink-0 text-right" />
          </li>
        ))}
        <li className="flex items-center justify-between gap-2 bg-muted px-4 py-2.5 text-[13px] font-medium">
          <span>Total, including {issue.key} itself ({usd(issue.usage.cost_usd)})</span>
          <span className="tnum font-semibold">{usd(issue.total_cost_usd)}</span>
        </li>
      </ul>
      <div className="max-sm:hidden">
        <Table className="min-w-[760px]">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>Issue</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Waits for</TableHead>
              <TableHead>Change request</TableHead>
              <TableHead>Latest run</TableHead>
              <TableHead className="text-right">Cost</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map(({ issue: c, depth }) => {
              const waits = c.depends_on.filter((d) => !isFinished(status.get(d) ?? ""));
              return (
                <TableRow key={c.key} className={isFinished(c.status) ? "text-muted-fg" : undefined}>
                  <TableCell className="min-w-72">
                    <div className="flex items-start gap-2" style={{ paddingLeft: depth * 18 }}>
                      <span className="mt-0.5"><TypeIcon type={c.type} /></span>
                      <IssueLink issueKey={c.key} title={c.title} className="[overflow-wrap:anywhere]" />
                    </div>
                  </TableCell>
                  <TableCell><StatusBadge status={c.status} /></TableCell>
                  <TableCell className="text-xs">
                    {waits.length ? waits.map((d, n) => <span key={d}>{n > 0 && ", "}<IssueLink issueKey={d} /></span>) : <span className="text-muted-fg">-</span>}
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap">
                    {c.change_request
                      ? <a href={c.change_request.url} target="_blank" rel="noreferrer" className="text-primary hover:underline">#{c.change_request.number} <span className="text-muted-fg">{c.change_request.status}</span></a>
                      : <span className="text-muted-fg">-</span>}
                  </TableCell>
                  <TableCell>
                    {c.latest_run
                      ? <AppLink href={paths.run(c.latest_run.id)} aria-label={`latest run of ${c.key}`}><StatusBadge status={c.latest_run.status} /></AppLink>
                      : <span className="text-xs text-muted-fg">-</span>}
                  </TableCell>
                  <TableCell className="text-right whitespace-nowrap">
                    <Cost own={c.cost_usd} total={c.total_cost_usd} parent={parents.has(c.key)} />
                  </TableCell>
                </TableRow>
              );
            })}
            <TableRow className="bg-muted hover:bg-muted">
              <TableCell colSpan={5} className="font-medium">Total, including {issue.key} itself ({usd(issue.usage.cost_usd)})</TableCell>
              <TableCell className="tnum text-right font-semibold">{usd(issue.total_cost_usd)}</TableCell>
            </TableRow>
          </TableBody>
        </Table>
      </div>
    </Card>
  );
}

// IssueChildrenShort is the Overview's short list: the issues right under the
// parent, with the whole tree one click away on the Issues tab.
export function IssueChildrenShort({ issue }: { issue: IssueDetail }) {
  return (
    <Card className="overflow-hidden">
      <CardHeader className="flex-wrap">
        <CardTitle>Issues under it</CardTitle>
        <AppLink href={paths.issue(issue.key, "issues")} className="inline-flex items-center gap-1 text-xs text-primary hover:underline">
          All issues and their cost<ArrowRightIcon className="size-3" aria-hidden="true" />
        </AppLink>
      </CardHeader>
      <ul className="divide-y divide-line">
        {issue.children.map((c) => (
          <li key={c.key} className="flex items-start gap-2 px-4 py-2 text-[13px]">
            <span className="mt-0.5"><TypeIcon type={c.type} /></span>
            <IssueLink issueKey={c.key} title={c.title} className="flex-1 [overflow-wrap:anywhere]" />
            <StatusBadge status={c.status} />
          </li>
        ))}
      </ul>
    </Card>
  );
}
