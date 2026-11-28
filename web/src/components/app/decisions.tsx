// The decisions a person makes, used by the approvals inbox and by the
// banner on an issue page: a human gate (docs/spec/03, D92), an agent's
// question (D55) and a refined spec (D64). Each sends its write, shows the
// API's error next to its buttons, and leaves the item to disappear when the
// queries refresh.

import { CheckIcon, SendIcon, Undo2Icon, XIcon } from "lucide-react";
import { useState, type ReactNode } from "react";
import { useAnswerQuestion, useDecideGate, useDecideSpec } from "@/api/mutations";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { toast } from "@/components/ui/toast";
import { approvalHelp, type Approval, type Decision } from "@/approvals";
import type { Question } from "@/questions";

function WriteError({ error }: { error: Error | null }) {
  return error ? <p role="alert" className="text-xs text-bad">{error.message}</p> : null;
}

const decided: Record<Decision, string> = { approve: "Approved", reject: "Rejected", "request-changes": "Changes requested" };

// GateDecision approves or rejects a human gate, or requests changes where
// the gate takes it; a comment goes with the decision, and requesting
// changes needs one (the coder gets it).
export function GateDecision({ approval, children }: { approval: Pick<Approval, "step_run_id" | "can_request_changes" | "step" | "issue_key">; children?: ReactNode }) {
  const help = approvalHelp(approval);
  const [comment, setComment] = useState("");
  const m = useDecideGate();
  const send = (decision: Decision) =>
    m.mutate({ gateID: approval.step_run_id, decision, comment }, {
      onSuccess: () => toast(`${decided[decision]}: ${approval.issue_key ? `${approval.issue_key} · ` : ""}${approval.step}.`),
    });
  const busy = m.isPending || m.isSuccess;
  return (
    <div className="grid gap-2">
      <Textarea aria-label="Comment" rows={2} maxLength={4000} placeholder={help.placeholder} value={comment} disabled={busy} onChange={(e) => setComment(e.target.value)} />
      <div className="flex flex-wrap items-center gap-2">
        <Button disabled={busy} onClick={() => send("approve")}><CheckIcon className="size-3.5" />Approve</Button>
        {approval.can_request_changes && (
          <Button variant="outline" disabled={busy || comment.trim() === ""} onClick={() => send("request-changes")}><Undo2Icon className="size-3.5" />Request changes</Button>
        )}
        <Button variant="destructive" disabled={busy} onClick={() => send("reject")}><XIcon className="size-3.5" />{help.reject}</Button>
        {children}
        {m.isSuccess && <span className="text-xs text-muted-fg">Sent; the run continues.</span>}
      </div>
      <WriteError error={m.error} />
    </div>
  );
}

// QuestionAnswer answers an agent's question; the agent gets the answer as
// its tool's result, and later steps of the issue see it (D56).
export function QuestionAnswer({ question, children }: { question: Pick<Question, "id" | "issue_key">; children?: ReactNode }) {
  const [text, setText] = useState("");
  const m = useAnswerQuestion();
  const busy = m.isPending || m.isSuccess;
  return (
    <div className="grid gap-2">
      <div className="flex flex-col gap-2 sm:flex-row">
        <Textarea aria-label="Your answer" rows={2} maxLength={8000} placeholder="Your answer goes to the agent and to later steps of the issue"
          value={text} disabled={busy} onChange={(e) => setText(e.target.value)} className="flex-1" />
        <Button className="self-end" disabled={busy || text.trim() === ""}
          onClick={() => m.mutate({ questionID: question.id, text }, { onSuccess: () => toast(`Answered the question on ${question.issue_key}.`) })}>
          <SendIcon className="size-3.5" />Answer
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {children}
        {m.isSuccess && <span className="text-xs text-muted-fg">Sent; the agent continues.</span>}
      </div>
      <WriteError error={m.error} />
    </div>
  );
}

// SpecDecision approves a refined spec (it becomes the issue's, and the issue
// starts once its dependencies are done) or sends it back to the refiner,
// which needs feedback saying what to change.
export function SpecDecision({ issueKey, children }: { issueKey: string; children?: ReactNode }) {
  const [comment, setComment] = useState("");
  const m = useDecideSpec();
  const busy = m.isPending || m.isSuccess;
  return (
    <div className="grid gap-2">
      <Textarea aria-label="Feedback for the refiner" rows={2} maxLength={4000} placeholder="Feedback (required to send it back: what the refiner should change)"
        value={comment} disabled={busy} onChange={(e) => setComment(e.target.value)} />
      <div className="flex flex-wrap items-center gap-2">
        <Button disabled={busy} onClick={() => m.mutate({ key: issueKey, decision: "approve", comment }, { onSuccess: () => toast(`Approved the spec of ${issueKey}.`) })}>
          <CheckIcon className="size-3.5" />Approve spec
        </Button>
        <Button variant="outline" disabled={busy || comment.trim() === ""}
          onClick={() => m.mutate({ key: issueKey, decision: "reject", comment }, { onSuccess: () => toast(`Sent the spec of ${issueKey} back to the refiner.`) })}>
          <Undo2Icon className="size-3.5" />Send back with feedback
        </Button>
        {children}
      </div>
      <WriteError error={m.error} />
    </div>
  );
}
