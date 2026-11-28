import { CheckIcon, ExternalLinkIcon, GitPullRequestIcon, PlayIcon, RotateCwIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { useCancelIssue, useCompleteIssue, useRetryIssue, useStartIssues } from "@/api/mutations";
import { sourceLabel } from "@/activity";
import { IssueLink } from "@/components/app/IssueLink";
import { StartReplyPanel } from "@/components/app/StartReplyPanel";
import { WriteError } from "@/components/app/states";
import { StatusBadge } from "@/components/app/StatusBadge";
import { TypeIcon } from "@/components/app/TypeIcon";
import { Alert } from "@/components/ui/alert";
import {
  AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";
import { formatWhen } from "@/format";
import { toast } from "@/components/ui/toast";
import { issueActions, type CloseReply, type IssueDetail, type StartReply } from "@/issues";

// CancelDialog cancels the issue and everything unfinished under it, with a
// reason that is also posted on the provider issues (D70, D71).
function CancelDialog({ issue, open, onOpenChange, onClosed }: {
  issue: IssueDetail; open: boolean; onOpenChange: (o: boolean) => void; onClosed: (r: CloseReply) => void;
}) {
  const [reason, setReason] = useState("");
  const m = useCancelIssue();
  const parent = issue.children.length > 0;
  return (
    <Dialog open={open} onOpenChange={(o) => { if (!m.isPending) onOpenChange(o); }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Cancel {issue.key}</DialogTitle>
          <DialogDescription>
            {parent ? "It and every unfinished issue under it move" : "It moves"} to canceled, and Frankie closes the GitHub
            {parent ? " issues" : " issue"} as not planned, with this reason. A run in progress has to end first.
          </DialogDescription>
        </DialogHeader>
        <Textarea aria-label="Why is it canceled?" placeholder="Why is it canceled? (posted on GitHub)" rows={3} maxLength={2000}
          value={reason} disabled={m.isPending} onChange={(e) => setReason(e.target.value)} />
        <WriteError error={m.error} />
        <DialogFooter>
          <Button variant="outline" disabled={m.isPending} onClick={() => onOpenChange(false)}>Keep it</Button>
          <Button variant="destructive" disabled={m.isPending || reason.trim() === ""}
            onClick={() => m.mutate({ key: issue.key, reason }, {
              onSuccess: (r) => { onClosed(r); setReason(""); onOpenChange(false); toast(`Canceled ${issue.key}.`); },
            })}>
            <XIcon className="size-3.5" />Cancel issue
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// CompleteDialog confirms completing a parent whose issues are all finished (D70).
function CompleteDialog({ issue, open, onOpenChange, onClosed }: {
  issue: IssueDetail; open: boolean; onOpenChange: (o: boolean) => void; onClosed: (r: CloseReply) => void;
}) {
  const m = useCompleteIssue();
  return (
    <AlertDialog open={open} onOpenChange={(o) => { if (!m.isPending) onOpenChange(o); }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Complete {issue.key}?</AlertDialogTitle>
          <AlertDialogDescription>It moves to merged, and Frankie closes its GitHub issue as completed.</AlertDialogDescription>
        </AlertDialogHeader>
        <WriteError error={m.error} />
        <AlertDialogFooter>
          <AlertDialogCancel disabled={m.isPending}>Keep it open</AlertDialogCancel>
          <Button disabled={m.isPending}
            onClick={() => m.mutate(issue.key, { onSuccess: (r) => { onClosed(r); onOpenChange(false); toast(`Completed ${issue.key}.`); } })}>
            <CheckIcon className="size-3.5" />Complete
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

// IssueHeader is the top of an issue's page: its type, key and source, its
// title and status, and only the actions that do something now (D72), with
// what the issue waits for when there is nothing to do.
export function IssueHeader({ issue }: { issue: IssueDetail }) {
  const actions = issueActions(issue);
  const start = useStartIssues();
  const retry = useRetryIssue();
  const [reply, setReply] = useState<StartReply | null>(null);
  // What a cancel or complete closed besides the issue: the unfinished
  // issues under a parent.
  const [closed, setClosed] = useState<CloseReply | null>(null);
  const [dialog, setDialog] = useState<"cancel" | "complete" | null>(null);
  const busy = start.isPending || retry.isPending;
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-fg">
            <TypeIcon type={issue.type} />
            <span className="mono">{issue.key}</span>
            <span aria-hidden="true">·</span>
            <span>{issue.type}</span>
            {issue.source_ref && (
              <>
                <span aria-hidden="true">·</span>
                <a href={issue.source_ref} target="_blank" rel="noreferrer" className="inline-flex min-w-0 items-center gap-1 hover:text-fg [overflow-wrap:anywhere]">
                  {sourceLabel(issue.source_ref)}<ExternalLinkIcon className="size-3 shrink-0" aria-hidden="true" />
                </a>
              </>
            )}
          </div>
          <h1 className="mt-1 text-xl font-semibold tracking-tight [overflow-wrap:anywhere]">
            {issue.title} <StatusBadge status={issue.status} className="ml-1 align-middle" />
          </h1>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {actions.start && (
            <Button disabled={busy} onClick={() => start.mutate([issue.key], { onSuccess: (r) => { setReply(r); toast(`Start requested: ${issue.key}.`); } })}>
              <PlayIcon className="size-3.5" />{actions.start}
            </Button>
          )}
          {actions.retry && (
            <Button disabled={busy} onClick={() => retry.mutate(issue.key, { onSuccess: () => toast(`Retrying ${issue.key}.`) })}>
              <RotateCwIcon className="size-3.5" />Retry
            </Button>
          )}
          {actions.complete && <Button variant="outline" onClick={() => setDialog("complete")}><CheckIcon className="size-3.5" />Complete</Button>}
          {actions.changeRequest && (
            <Button asChild variant="outline">
              <a href={actions.changeRequest} target="_blank" rel="noreferrer"><GitPullRequestIcon className="size-3.5" />Review change request</a>
            </Button>
          )}
          {actions.cancel && <Button variant="destructive" onClick={() => setDialog("cancel")}><XIcon className="size-3.5" />Cancel…</Button>}
        </div>
      </div>
      {actions.note && <p className="text-[13px] text-muted-fg">{actions.note}.</p>}
      <WriteError error={start.error ?? retry.error} />
      {reply && <StartReplyPanel reply={reply} onDismiss={() => setReply(null)} />}
      {closed && closed.closed.length > 1 && (
        <Alert tone="ok" className="items-center justify-between">
          <p>
            Closed: {closed.closed.map((c, n) => <span key={c.key}>{n > 0 && ", "}<IssueLink issueKey={c.key} /> ({c.status})</span>)}.
            {" "}Frankie closes their GitHub issues within a minute.
          </p>
          <Button variant="ghost" size="icon-sm" aria-label="Dismiss" onClick={() => setClosed(null)}><XIcon className="size-3.5" /></Button>
        </Alert>
      )}
      {issue.closed_at && (
        <Alert tone={issue.status === "canceled" ? "default" : "ok"}>
          <p className="[overflow-wrap:anywhere]">
            {issue.status === "canceled" ? "Canceled" : "Completed"} by {issue.closed_by || "someone"} on {formatWhen(issue.closed_at)}
            {issue.close_reason && <>: {issue.close_reason}</>}
          </p>
        </Alert>
      )}
      <CancelDialog issue={issue} open={dialog === "cancel"} onOpenChange={(o) => setDialog(o ? "cancel" : null)} onClosed={setClosed} />
      <CompleteDialog issue={issue} open={dialog === "complete"} onOpenChange={(o) => setDialog(o ? "complete" : null)} onClosed={setClosed} />
    </div>
  );
}
