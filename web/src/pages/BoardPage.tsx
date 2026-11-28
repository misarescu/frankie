import { useQuery } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { AlertTriangleIcon, CheckCheckIcon, GitForkIcon, InboxIcon, ListTreeIcon, PlayIcon } from "lucide-react";
import { useState } from "react";
import { approvalsQuery, issuesQuery, questionsQuery, releasesQuery, runsQuery, specsQuery } from "@/api/queries";
import { useStartIssues } from "@/api/mutations";
import { BoardGraph } from "@/components/app/board-graph";
import { StartReplyPanel } from "@/components/app/board-start";
import { BoardTree } from "@/components/app/board-tree";
import { AppLink } from "@/components/app/AppLink";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { PageHeader } from "@/components/app/PageHeader";
import { useProject } from "@/components/app/project";
import { Stat } from "@/components/app/Stat";
import { EmptyState, ErrorState, LoadingState } from "@/components/app/states";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Segmented } from "@/components/ui/segmented";
import { boardRows, boardSummary, graphIssues, type Inbox } from "@/board";
import { projectCrumb } from "@/crumbs";
import type { Project, StartReply } from "@/issues";
import { paths } from "@/paths";
import { usd } from "@/steps";

const route = getRouteApi("/projects/$project/board");

// BoardPage is a project's issues (M5, M9): a summary, the hierarchy as a
// tree or the dependency graph, and starting what is selected. A new project
// starts with nothing selected.
export function BoardPage() {
  const project = useProject();
  useCrumbs([projectCrumb(project), { label: "Board" }]);
  return <Board key={project.name} project={project} />;
}

function Board({ project }: { project: Project }) {
  const search = route.useSearch();
  const navigate = route.useNavigate();
  const view = search.view ?? "list";
  const finished = search.finished === 1;
  const issues = useQuery(issuesQuery(project.name));
  const approvals = useQuery(approvalsQuery());
  const questions = useQuery(questionsQuery());
  const specs = useQuery(specsQuery());
  const runs = useQuery(runsQuery());
  const releases = useQuery(releasesQuery(project.name));
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const [reply, setReply] = useState<StartReply | null>(null);
  const start = useStartIssues();

  const inbox: Inbox | undefined = approvals.data && questions.data && specs.data
    ? { approvals: approvals.data, questions: questions.data, specs: specs.data }
    : undefined;
  const flip = (set: ReadonlySet<string>, key: string, on: boolean) => {
    const next = new Set(set);
    if (on) next.add(key);
    else next.delete(key);
    return next;
  };
  const startSelected = () => {
    setReply(null);
    start.mutate([...selected], {
      onSuccess: (r) => { setReply(r); setSelected(new Set()); },
    });
  };
  const setFinished = (on: boolean) => void navigate({ search: (s) => ({ ...s, finished: on ? 1 : undefined }) });

  const header = (
    <PageHeader
      title="Board"
      sub={<span className="[overflow-wrap:anywhere]">{project.name} · its issues as they nest, with what each waits for</span>}
      actions={
        <>
          <Segmented
            label="View"
            value={view}
            onValueChange={(v) => void navigate({ search: (s) => ({ ...s, view: v === "graph" ? "graph" : undefined }) })}
            options={[
              { value: "list", label: "List", icon: <ListTreeIcon className="size-3.5" /> },
              { value: "graph", label: "Dependencies", icon: <GitForkIcon className="size-3.5" /> },
            ]}
          />
          <Button disabled={selected.size === 0 || start.isPending} onClick={startSelected}>
            <PlayIcon className="size-3.5" />
            {selected.size > 0 ? `Start ${selected.size} selected` : "Start selected"}
          </Button>
        </>
      }
    />
  );

  if (issues.isPending) return <div className="space-y-5">{header}<LoadingState rows={6} /></div>;
  if (!issues.data) {
    return <div className="space-y-5">{header}<ErrorState error={issues.error} what="the issues" onRetry={() => void issues.refetch()} /></div>;
  }

  const all = issues.data;
  const rows = boardRows(all, { finished, collapsed });
  const summary = boardSummary(project, all, inbox, runs.data && releases.data ? { runs: runs.data, releases: releases.data } : undefined);
  const hiddenFinished = !finished && rows.length === 0 && all.length > 0;

  return (
    <div className="space-y-5">
      {header}
      {start.error && (
        <Alert tone="bad">
          <AlertTriangleIcon className="size-4" />
          <div className="min-w-0 [overflow-wrap:anywhere]">Could not start {[...selected].join(", ")}: {start.error.message}</div>
        </Alert>
      )}
      {reply && <StartReplyPanel reply={reply} onDismiss={() => setReply(null)} />}
      {issues.isError && (
        <Alert tone="warn">
          <AlertTriangleIcon className="size-4" />
          <div className="min-w-0 [overflow-wrap:anywhere]">Could not refresh the issues ({issues.error.message}); showing what was loaded last.</div>
        </Alert>
      )}
      <Card className="grid grid-cols-2 sm:grid-cols-4 sm:divide-x sm:divide-line">
        <Stat label="Running" value={summary.running} sub="issues with a run going" tone={summary.running ? "primary" : undefined} />
        <AppLink href={paths.approvals()} className="rounded-lg hover:bg-hover">
          <Stat label="Waiting for you" value={inbox ? summary.waiting : "-"} sub="gates, questions, specs" tone={summary.waiting ? "warn" : undefined} />
        </AppLink>
        <Stat label="Merged" value={summary.merged} sub="issues on main" />
        <Stat label="Spent" value={usd(summary.spent)} sub="all issues, rolled up" />
      </Card>
      {all.length === 0 ? (
        <EmptyState icon={<InboxIcon className="size-6" />} title="No issues yet">
          Issues appear here once they sync from {project.name}: give a GitHub issue the project's factory label and Frankie picks it up, with its sub-issues and blockers.
        </EmptyState>
      ) : view === "graph" ? (
        <BoardGraph issues={graphIssues(all, finished)} />
      ) : (
        <Card className="overflow-hidden">
          <div className="flex flex-wrap items-center justify-between gap-2 border-b border-line px-3 py-2">
            <div className="flex flex-wrap items-center gap-x-2 text-xs text-muted-fg">
              <span>{rows.length} {rows.length === 1 ? "issue" : "issues"}</span>
              <span aria-hidden="true" className="max-sm:hidden">·</span>
              <span>epics and stories show the cost of everything under them</span>
            </div>
            <label htmlFor="show-finished" className="flex cursor-pointer items-center gap-2 text-xs text-muted-fg">
              <Checkbox id="show-finished" checked={finished} onCheckedChange={(v) => setFinished(v === true)} />
              Show finished
            </label>
          </div>
          {hiddenFinished ? (
            <EmptyState
              className="rounded-none border-0"
              icon={<CheckCheckIcon className="size-6" />}
              title="Everything here is finished"
              action={<Button variant="outline" size="sm" onClick={() => setFinished(true)}>Show finished</Button>}
            >
              Every issue of {project.name} is merged, released or canceled.
            </EmptyState>
          ) : (
            <BoardTree
              rows={rows}
              issues={all}
              inbox={inbox}
              selected={selected}
              onSelect={(k, on) => setSelected((s) => flip(s, k, on))}
              collapsed={collapsed}
              onCollapse={(k) => setCollapsed((s) => flip(s, k, !s.has(k)))}
            />
          )}
        </Card>
      )}
    </div>
  );
}
