import { useQuery } from "@tanstack/react-query";
import { ExternalLinkIcon, FileTextIcon, MessageCircleQuestionIcon, ShieldAlertIcon } from "lucide-react";
import type { ReactNode } from "react";
import { approvalsQuery, questionsQuery } from "@/api/queries";
import { AppLink } from "@/components/app/AppLink";
import { GateDecision, QuestionAnswer, SpecDecision } from "@/components/app/decisions";
import { Button } from "@/components/ui/button";
import type { IssueDetail } from "@/issues";
import { paths, type ApprovalKind } from "@/paths";

function Banner({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <section aria-label={title} className="space-y-3 rounded-lg border border-warn/40 bg-warn-soft px-4 py-3">
      <div className="flex min-w-0 items-start gap-3">
        <span className="mt-0.5 shrink-0 text-warn">{icon}</span>
        <div className="min-w-0 flex-1 space-y-1">
          <h2 className="text-[13px] font-semibold [overflow-wrap:anywhere]">{title}</h2>
          {children}
        </div>
      </div>
    </section>
  );
}

const openInbox = (kind: ApprovalKind) => (
  <Button asChild variant="outline"><AppLink href={paths.approvals(kind)}>Open in Approvals</AppLink></Button>
);

// IssueWaiting shows what waits for a person on this issue, decided in
// place: a human gate of its run (D43, D92), an agent's question (D55) and a
// refined spec (D64). The approvals inbox lists the same across projects.
export function IssueWaiting({ issue }: { issue: IssueDetail }) {
  const gates = (useQuery(approvalsQuery()).data ?? []).filter((a) => a.issue_key === issue.key);
  const questions = (useQuery(questionsQuery()).data ?? []).filter((q) => q.issue_key === issue.key);
  const proposal = issue.status === "refining" ? issue.spec_proposal : undefined;
  if (gates.length === 0 && questions.length === 0 && !proposal) return null;
  return (
    <div className="space-y-3">
      {gates.map((a) => (
        <Banner key={a.step_run_id} icon={<ShieldAlertIcon className="size-4" />} title={`Gate ${a.step} waits for your decision`}>
          <p className="text-[13px] text-muted-fg [overflow-wrap:anywhere]">{a.reason}</p>
          <p className="flex flex-wrap gap-x-3 text-xs text-muted-fg">
            {a.commit_sha && <span>at <span className="mono">{a.commit_sha.slice(0, 7)}</span></span>}
            <AppLink href={paths.run(a.run_id, a.step_run_id)} className="text-primary hover:underline">its run</AppLink>
            {a.change_request_url && (
              <a href={a.change_request_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline">
                change request<ExternalLinkIcon className="size-3" aria-hidden="true" />
              </a>
            )}
          </p>
          <div className="pt-1"><GateDecision approval={a}>{openInbox("gate")}</GateDecision></div>
        </Banner>
      ))}
      {questions.map((q) => (
        <Banner key={q.id} icon={<MessageCircleQuestionIcon className="size-4" />} title={`The agent of ${q.step} asks`}>
          <p className="text-[13.5px] whitespace-pre-wrap [overflow-wrap:anywhere]">{q.question}</p>
          <p className="text-xs text-muted-fg">
            asked {new Date(q.created_at).toLocaleString()} · <AppLink href={paths.run(q.run_id, q.step_run_id)} className="text-primary hover:underline">its run</AppLink>
          </p>
          <div className="pt-1"><QuestionAnswer question={q}>{openInbox("question")}</QuestionAnswer></div>
        </Banner>
      ))}
      {proposal && (
        <Banner icon={<FileTextIcon className="size-4" />} title="A refined spec waits for approval">
          {proposal.summary && <p className="text-[13px] text-muted-fg [overflow-wrap:anywhere]"><span className="font-medium text-fg">The refiner: </span>{proposal.summary}</p>}
          <div className="max-h-96 overflow-y-auto rounded-md border border-line bg-card px-3 py-2">
            <p className="max-w-[72ch] text-[13.5px] leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]">{proposal.spec}</p>
            {proposal.acceptance_criteria.length > 0 && (
              <>
                <h3 className="mt-3 text-xs font-medium text-muted-fg">Acceptance criteria</h3>
                <ol className="mt-1 list-decimal space-y-0.5 pl-5 text-[13px]">
                  {proposal.acceptance_criteria.map((c, n) => <li key={n} className="[overflow-wrap:anywhere]">{c}</li>)}
                </ol>
              </>
            )}
            {(proposal.file_hints?.length ?? 0) > 0 && (
              <p className="mt-2 text-xs text-muted-fg">touches <span className="mono [overflow-wrap:anywhere]">{proposal.file_hints!.join(", ")}</span></p>
            )}
          </div>
          <div className="pt-1"><SpecDecision issueKey={issue.key}>{openInbox("spec")}</SpecDecision></div>
        </Banner>
      )}
    </div>
  );
}
