import { ChevronDownIcon, ExternalLinkIcon, FileTextIcon, GavelIcon, GitPullRequestIcon, MessageCircleQuestionIcon, RocketIcon, ShieldAlertIcon } from "lucide-react";
import type { ReactNode } from "react";
import { AppLink } from "@/components/app/AppLink";
import { GateDecision, QuestionAnswer, SpecDecision } from "@/components/app/decisions";
import { IssueLink } from "@/components/app/IssueLink";
import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { expiresIn, type Approval } from "@/approvals";
import { ago, shortSHA } from "@/format";
import { gateTitle, isProtected, releaseGateSHA } from "@/inbox";
import { cn } from "@/lib/utils";
import { paths } from "@/paths";
import type { Question } from "@/questions";
import type { Spec } from "@/specs";

const link = "inline-flex items-center gap-1 text-xs text-primary hover:underline";

// InboxCard is one item of the approvals inbox (the mockup's card): what kind
// it is and how long it waited, what it asks, where it comes from, why, and
// the decision beneath. Each is a section named by its key, so it can be
// found by what it is about.
function InboxCard({ label, icon, tone = "warn", kind, meta, title, links, children, footer }: {
  label: string; icon: ReactNode; tone?: "warn" | "bad"; kind: string; meta: ReactNode[]; title: string;
  links: ReactNode; children?: ReactNode; footer: ReactNode;
}) {
  return (
    <section aria-label={label} className="min-w-0 overflow-hidden rounded-lg border border-line bg-card">
      <div className="flex items-start gap-3 px-4 py-3">
        <span className={cn("mt-0.5 shrink-0 rounded-md p-1.5 [&_svg]:size-4", tone === "bad" ? "bg-bad-soft text-bad" : "bg-warn-soft text-warn")}>{icon}</span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-fg">
            <span className="font-medium uppercase tracking-wider">{kind}</span>
            {meta.map((m, n) => <span key={n} className="flex items-center gap-2"><span aria-hidden="true">·</span>{m}</span>)}
          </div>
          <h3 className="mt-0.5 text-[14px] font-semibold [overflow-wrap:anywhere]">{title}</h3>
          <div className="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[13px] [overflow-wrap:anywhere]">{links}</div>
          {children}
        </div>
      </div>
      <div className="border-t border-line bg-muted px-4 py-3">{footer}</div>
    </section>
  );
}

const Reason = ({ children }: { children: ReactNode }) => (
  <p className="mt-2 max-w-[75ch] text-[13px] leading-relaxed whitespace-pre-wrap text-muted-fg [overflow-wrap:anywhere]">{children}</p>
);

// GateCard is a human gate: on an issue (protected changes, a merge approval
// with request changes, a bug-fix decision) or before a release stage (D139),
// which links to its release instead of an issue.
export function GateCard({ approval: a, release }: { approval: Approval; release?: { id?: string; name?: string } }) {
  const sha = releaseGateSHA(a);
  const prot = !sha && isProtected(a);
  const expiry = expiresIn(a.expires_at);
  return (
    <InboxCard
      label={a.issue_key}
      icon={sha ? <RocketIcon /> : prot ? <ShieldAlertIcon /> : <GavelIcon />}
      tone={prot ? "bad" : "warn"}
      kind={sha ? "Release gate" : prot ? "Protected paths" : "Human gate"}
      meta={[<span title={a.created_at}>waiting {ago(a.created_at)}</span>, <span className={cn(expiry === "expired" && "text-bad")} title={a.expires_at}>{expiry === "expired" ? "expired" : `expires in ${expiry}`}</span>]}
      title={gateTitle(a, release?.name)}
      links={
        <>
          {sha
            ? release?.id
              ? <AppLink href={paths.release(release.id)} className="text-primary hover:underline">{a.issue_title}</AppLink>
              : <span>{a.issue_title}</span>
            : <IssueLink issueKey={a.issue_key} title={a.issue_title} />}
          <AppLink href={paths.run(a.run_id, a.step_run_id)} className={link}>Run · {a.step}</AppLink>
          {a.change_request_url && (
            <a href={a.change_request_url} target="_blank" rel="noreferrer" className={link}>
              <GitPullRequestIcon className="size-3.5" />Review the change request<ExternalLinkIcon className="size-3" />
            </a>
          )}
          {a.commit_sha && <span className="text-xs text-muted-fg">at <span className="mono" title={a.commit_sha}>{shortSHA(a.commit_sha)}</span></span>}
        </>
      }
      footer={<GateDecision approval={a} />}
    >
      <Reason>{a.reason}</Reason>
    </InboxCard>
  );
}

// QuestionCard is an agent's question (D55); the agent waits for the answer.
export function QuestionCard({ question: q }: { question: Question }) {
  return (
    <InboxCard
      label={q.issue_key}
      icon={<MessageCircleQuestionIcon />}
      kind="Question from an agent"
      meta={[<span title={q.created_at}>asked {ago(q.created_at)}</span>, <span>the agent is paused until you answer</span>]}
      title={`The ${q.step} step of ${q.issue_key} asks`}
      links={
        <>
          <IssueLink issueKey={q.issue_key} title={q.issue_title} />
          <AppLink href={paths.run(q.run_id, q.step_run_id)} className={link}>Run · {q.step}</AppLink>
        </>
      }
      footer={<QuestionAnswer question={q} />}
    >
      <p className="mt-2 max-w-[75ch] border-l-2 border-warn pl-3 text-[13px] leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]">{q.question}</p>
    </InboxCard>
  );
}

// SpecCard is a refined spec waiting for approval before its issue runs
// (D64): the refiner's summary, the spec it wrote with its acceptance
// criteria and the files it expects to touch, and the original text.
export function SpecCard({ spec: s }: { spec: Spec }) {
  return (
    <InboxCard
      label={s.key}
      icon={<FileTextIcon />}
      kind="Spec approval"
      meta={[<span>{s.type} in {s.project}</span>, ...(s.requested_by ? [<span>started by {s.requested_by}</span>] : [])]}
      title={`Approve the refined spec of ${s.key}`}
      links={
        <>
          <IssueLink issueKey={s.key} title={s.title} />
          {s.run_id && <AppLink href={paths.run(s.run_id)} className={link}>Refinement run</AppLink>}
          {s.source_ref && <a href={s.source_ref} target="_blank" rel="noreferrer" className={link}>Source<ExternalLinkIcon className="size-3" /></a>}
        </>
      }
      footer={
        <SpecDecision issueKey={s.key}>
          <Button asChild variant="outline"><AppLink href={paths.issue(s.key)}>Open {s.key}</AppLink></Button>
        </SpecDecision>
      }
    >
      {s.summary && <Reason>{s.summary}</Reason>}
      <div className="mt-3 space-y-3 text-[13px]">
        <div>
          <h4 className="text-xs2 font-medium uppercase tracking-wider text-muted-fg">Refined spec</h4>
          <div className="scroll-thin mt-1 max-h-72 overflow-y-auto rounded-md bg-code px-3 py-2 leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]">
            {s.spec || <span className="text-muted-fg">(no description)</span>}
          </div>
        </div>
        {s.acceptance_criteria.length > 0 && (
          <div>
            <h4 className="text-xs2 font-medium uppercase tracking-wider text-muted-fg">Acceptance criteria</h4>
            <ul className="mt-1 list-disc space-y-0.5 pl-5 [overflow-wrap:anywhere]">{s.acceptance_criteria.map((c, i) => <li key={i}>{c}</li>)}</ul>
          </div>
        )}
        {s.file_hints.length > 0 && (
          <p className="flex flex-wrap items-center gap-1.5 text-xs text-muted-fg">
            touches {s.file_hints.map((f) => <span key={f} className="mono rounded bg-muted px-1.5 py-0.5 text-fg [overflow-wrap:anywhere]">{f}</span>)}
          </p>
        )}
        <Collapsible>
          <CollapsibleTrigger className="group inline-flex cursor-pointer items-center gap-1 text-xs font-medium text-muted-fg hover:text-fg">
            <ChevronDownIcon className="size-3.5 transition-transform group-data-[state=closed]:-rotate-90" />
            Original text
          </CollapsibleTrigger>
          <CollapsibleContent className="scroll-thin mt-1 max-h-72 overflow-y-auto rounded-md border border-line px-3 py-2 whitespace-pre-wrap text-muted-fg [overflow-wrap:anywhere]">
            {s.original_spec || "(no description)"}
          </CollapsibleContent>
        </Collapsible>
      </div>
    </InboxCard>
  );
}
