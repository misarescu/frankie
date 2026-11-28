import { useQuery } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { approvalsQuery, questionsQuery, specsQuery } from "@/api/queries";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { GateDecision, QuestionAnswer, SpecDecision } from "@/components/app/decisions";
import { PageHeader } from "@/components/app/PageHeader";
import { useCurrentProject } from "@/components/app/project";
import { Card, CardContent, CardFooter } from "@/components/ui/card";
import { projectCrumb } from "@/crumbs";

const route = getRouteApi("/approvals");

// ApprovalsPage: a stub until its page is ported (M9). It lists what waits
// with the shared decision widgets, unstyled.
export function ApprovalsPage() {
  const search = route.useSearch();
  const { project } = useCurrentProject();
  const gates = useQuery(approvalsQuery()).data ?? [];
  const questions = useQuery(questionsQuery()).data ?? [];
  const specs = useQuery(specsQuery()).data ?? [];
  useCrumbs([...(project ? [projectCrumb(project)] : []), { label: "Approvals" }]);
  return (
    <div className="space-y-5">
      <PageHeader title="Approvals" sub={<span className="mono">{JSON.stringify({ search })}</span>} />
      {questions.map((q) => (
        <Card key={q.id}><CardContent>{q.issue_key} · {q.question}</CardContent><CardFooter><QuestionAnswer question={q} /></CardFooter></Card>
      ))}
      {gates.map((a) => (
        <Card key={a.step_run_id}><CardContent>{a.issue_key || "release"} · {a.step} · {a.reason}</CardContent><CardFooter><GateDecision approval={a} /></CardFooter></Card>
      ))}
      {specs.map((s) => (
        <Card key={s.key}><CardContent>{s.key} · {s.title}</CardContent><CardFooter><SpecDecision issueKey={s.key} /></CardFooter></Card>
      ))}
    </div>
  );
}
