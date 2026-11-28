import { useQuery } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { AlertTriangleIcon, FileQuestionIcon } from "lucide-react";
import { ApiError, issueQuery, issuesQuery } from "@/api/queries";
import { useIssueCrumbs } from "@/components/app/Breadcrumbs";
import { IssueActivity } from "@/components/app/issue-activity";
import { IssueChildren } from "@/components/app/issue-children";
import { IssueHeader } from "@/components/app/issue-header";
import { IssueOverview } from "@/components/app/issue-overview";
import { IssueRuns } from "@/components/app/issue-runs";
import { IssueWaiting } from "@/components/app/issue-waiting";
import { Stat } from "@/components/app/Stat";
import { EmptyState, ErrorState, LoadingState } from "@/components/app/states";
import { Alert } from "@/components/ui/alert";
import { Card } from "@/components/ui/card";
import { TabCount, Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { isFinished, leafStatuses, progress, subtree, type IssueDetail } from "@/issues";
import type { IssueTab } from "@/paths";
import { statusLabel } from "@/status";
import { usd } from "@/steps";

const route = getRouteApi("/issues/$key");

// Summary is the strip under the header (M9): what the issue cost (a parent
// its total, D142), its change request or a parent's progress, its runs, and
// what it waits for.
function Summary({ issue }: { issue: IssueDetail }) {
  const parent = issue.children.length > 0;
  const issues = useQuery({ ...issuesQuery(issue.project), enabled: parent }).data;
  const leaves = parent ? (issues ? leafStatuses(issues, issue.key) : issue.children.map((c) => c.status)) : [];
  const p = progress(leaves);
  const cr = issue.change_requests.at(-1);
  const waits = issue.depends_on.filter((d) => !isFinished(d.status));
  const own = issue.usage.cost_usd;
  return (
    <Card className="grid grid-cols-2 divide-line max-sm:[&>*:nth-child(-n+2)]:border-b max-sm:[&>*:nth-child(odd)]:border-r sm:grid-cols-4 sm:divide-x [&>*]:border-line">
      {parent
        ? <Stat label="Total cost" value={usd(issue.total_cost_usd)} sub={`own ${usd(own)}`} />
        : <Stat label="Cost" value={own > 0 ? usd(own) : "-"} sub={`${(issue.usage.input_tokens + issue.usage.output_tokens).toLocaleString("en-US")} tokens`} />}
      {parent
        ? <Stat label="Progress" value={`${p.merged} / ${p.total}`} sub={`issues merged${p.canceled ? `, ${p.canceled} canceled` : ""}`} />
        : <Stat label="Change request" value={cr ? <a href={cr.url} target="_blank" rel="noreferrer" className="text-primary hover:underline">#{cr.number}</a> : "-"}
            sub={cr ? statusLabel(cr.status) : "none yet"} />}
      <Stat label="Runs" value={issue.runs.length} sub={issue.runs[0] ? `latest: ${statusLabel(issue.runs[0].status)}` : "not started"} />
      <Stat label="Waits for" value={waits.length || "nothing"} tone={waits.length ? "warn" : undefined}
        sub={waits.length ? waits.map((d) => d.key).join(", ") : isFinished(issue.status) ? "finished" : issue.status === "draft" || issue.status === "ready" ? "can start now" : "its dependencies are done"} />
    </Card>
  );
}

// IssuePage is one issue (M9): its header and actions, what waits for a
// person on it, a summary, and the Overview, Runs, Issues (a parent's) and
// Activity tabs, each a search param so back, forward and reload keep it.
export function IssuePage() {
  const { key } = route.useParams();
  const { tab } = route.useSearch();
  const navigate = route.useNavigate();
  const q = useQuery(issueQuery(key));
  useIssueCrumbs(key, { title: q.data?.title });

  if (q.isPending) return <LoadingState rows={6} />;
  if (q.isError && !q.data) {
    if (q.error instanceof ApiError && q.error.status === 404) {
      return (
        <EmptyState level={1} icon={<FileQuestionIcon className="size-6" />} title={`No issue ${key}`}>
          No project has an issue with this key. It may be mistyped, or from a project that is no longer configured.
        </EmptyState>
      );
    }
    return <ErrorState level={1} error={q.error} what={`issue ${key}`} onRetry={() => void q.refetch()} />;
  }
  const issue = q.data;
  const parent = issue.children.length > 0;
  const shown: IssueTab = tab === "issues" && !parent ? "overview" : (tab ?? "overview");
  const go = (t: string) => void navigate({ search: (s) => ({ ...s, tab: t === "overview" ? undefined : (t as IssueTab) }) });
  return (
    <div className="space-y-5">
      {q.isError && (
        <Alert tone="bad"><AlertTriangleIcon className="size-4" />Could not refresh {key}: {q.error.message}. Showing what was loaded last.</Alert>
      )}
      <IssueHeader issue={issue} />
      <IssueWaiting issue={issue} />
      <Summary issue={issue} />
      <Tabs value={shown} onValueChange={go}>
        <TabsList aria-label={`${key} sections`}>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="runs">Runs<TabCount>{issue.runs.length}</TabCount></TabsTrigger>
          {parent && <TabsTrigger value="issues">Issues<IssueCount issue={issue} /></TabsTrigger>}
          <TabsTrigger value="activity">Activity</TabsTrigger>
        </TabsList>
        <TabsContent value="overview"><IssueOverview key={issue.key} issue={issue} /></TabsContent>
        <TabsContent value="runs"><IssueRuns issue={issue} /></TabsContent>
        {parent && <TabsContent value="issues"><IssueChildren issue={issue} /></TabsContent>}
        <TabsContent value="activity"><IssueActivity issue={issue} /></TabsContent>
      </Tabs>
    </div>
  );
}

// IssueCount is how many issues are under a parent, its whole subtree.
function IssueCount({ issue }: { issue: IssueDetail }) {
  const issues = useQuery(issuesQuery(issue.project)).data;
  return <TabCount>{issues ? subtree(issues, issue.key).rows.length : issue.children.length}</TabCount>;
}
