import { useQueries, useQuery } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { CheckCheckIcon, RotateCwIcon } from "lucide-react";
import { approvalsQuery, projectsQuery, questionsQuery, releasesQuery, runsQuery, specsQuery } from "@/api/queries";
import { GateCard, QuestionCard, SpecCard } from "@/components/app/approvals-cards";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { PageHeader } from "@/components/app/PageHeader";
import { useCurrentProject } from "@/components/app/project";
import { EmptyState, ErrorState, LoadingState } from "@/components/app/states";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Segmented } from "@/components/ui/segmented";
import { projectCrumb } from "@/crumbs";
import { emptyInbox, inboxCounts, inboxItems, ofKind, releaseGateSHA, releaseOf } from "@/inbox";
import type { ApprovalKind } from "@/paths";
import { releaseName } from "@/releases";

const route = getRouteApi("/approvals");

type Filter = ApprovalKind | "all";

const and = (xs: string[]) => (xs.length < 2 ? xs.join("") : `${xs.slice(0, -1).join(", ")} and ${xs.at(-1)}`);

// ApprovalsPage is the inbox (M9): every human gate, agent's question and
// refined spec waiting for a person, across all projects, filtered by kind
// in the URL. Each is decided in place and leaves once its write lands.
export function ApprovalsPage() {
  const { kind } = route.useSearch();
  const navigate = route.useNavigate();
  const { project } = useCurrentProject();
  useCrumbs([...(project ? [projectCrumb(project)] : []), { label: "Approvals" }]);

  const gates = useQuery(approvalsQuery());
  const questions = useQuery(questionsQuery());
  const specs = useQuery(specsQuery());
  const parts = [
    { q: questions, what: "the agents' questions" },
    { q: gates, what: "the gates" },
    { q: specs, what: "the specs" },
  ];

  // A release gate links to its release: found by its run, else by commit.
  const releaseGates = (gates.data ?? []).some((a) => releaseGateSHA(a));
  const runs = useQuery({ ...runsQuery(), enabled: releaseGates }).data ?? [];
  const projects = useQuery(projectsQuery()).data ?? [];
  const releases = useQueries({ queries: projects.map((p) => ({ ...releasesQuery(p.name), enabled: releaseGates })) })
    .flatMap((r) => r.data ?? []);

  const items = inboxItems(questions.data ?? [], gates.data ?? [], specs.data ?? []);
  const counts = inboxCounts(items);
  const shown = ofKind(items, kind);
  const filter: Filter = kind ?? "all";
  const loaded = parts.some((p) => p.q.data);
  const empty = emptyInbox(kind);

  const options: { value: Filter; label: string }[] = [
    { value: "all", label: "All" }, { value: "gate", label: "Gates" }, { value: "question", label: "Questions" }, { value: "spec", label: "Specs" },
  ];
  const header = (
    <PageHeader
      title="Approvals"
      sub="Everything that waits for a person, across all projects."
      actions={
        <Segmented<Filter>
          label="Show"
          value={filter}
          onValueChange={(v) => void navigate({ search: { kind: v === "all" ? undefined : v } })}
          options={options.map((o) => ({
            value: o.value,
            label: <>{o.label}{loaded && <span className="tnum rounded-full bg-muted px-1.5 text-xs2 text-muted-fg">{counts[o.value]}</span>}</>,
          }))}
        />
      }
    />
  );

  if (parts.some((p) => p.q.isPending)) return <div className="space-y-5">{header}<LoadingState rows={3} /></div>;
  const failed = parts.filter((p) => p.q.isError);
  const retry = () => { for (const p of failed) void p.q.refetch(); };
  if (failed.length === parts.length && parts.every((p) => !p.q.data)) {
    return <div className="space-y-5">{header}<ErrorState error={failed[0]!.q.error} what="the approvals inbox" onRetry={retry} /></div>;
  }

  return (
    <div className="space-y-5">
      {header}
      {failed.length > 0 && (
        <Alert tone="warn" className="flex-wrap items-center">
          <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">
            {[...new Set(failed.map((p) => p.q.error?.message))].map((m) => `Could not load ${and(failed.filter((p) => p.q.error?.message === m).map((p) => p.what))}: ${m}.`).join(" ")}
            {failed.some((p) => p.q.data) && " Showing what loaded last."}
          </span>
          <Button variant="outline" size="sm" onClick={retry}><RotateCwIcon className="size-3.5" />Try again</Button>
        </Alert>
      )}
      {shown.length === 0 && (
        <EmptyState icon={<CheckCheckIcon className="size-6 text-ok" />} title={empty.title}>{empty.body}</EmptyState>
      )}
      {shown.map((i) => {
        switch (i.kind) {
          case "question":
            return <QuestionCard key={i.id} question={i.question} />;
          case "spec":
            return <SpecCard key={i.id} spec={i.spec} />;
          case "gate": {
            const id = releaseOf(i.approval, runs, releases);
            const rel = releases.find((r) => r.id === id);
            return <GateCard key={i.id} approval={i.approval} release={id ? { id, name: rel && releaseName(rel) } : undefined} />;
          }
        }
      })}
    </div>
  );
}
