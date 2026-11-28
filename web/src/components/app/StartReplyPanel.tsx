import { XIcon } from "lucide-react";
import { IssueLink } from "@/components/app/IssueLink";
import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import type { StartReply } from "@/issues";

const keyList = (keys: string[]) => keys.map((k, n) => <span key={k}>{n > 0 && ", "}<IssueLink issueKey={k} /></span>);

// StartReplyPanel is the scheduler's plan for a start request (D62, D63),
// on the board and on the issue page: what starts on its next pass, what
// waits and why, and work in flight the started issues likely overlap.
export function StartReplyPanel({ reply, onDismiss }: { reply: StartReply; onDismiss: () => void }) {
  return (
    <Alert tone="primary" role="status" className="flex-col gap-1.5">
      <div className="flex w-full items-start justify-between gap-2">
        <p className="font-medium [overflow-wrap:anywhere]">Start requested: {keyList(reply.requested)}</p>
        <Button variant="ghost" size="icon-sm" aria-label="Dismiss" onClick={onDismiss}><XIcon className="size-3.5" /></Button>
      </div>
      {reply.starting.length > 0 && <p>Starting on the scheduler's next pass: {keyList(reply.starting)}</p>}
      {reply.waiting.length > 0 && (
        <ul className="list-disc pl-5 [overflow-wrap:anywhere]">
          {reply.waiting.map((w) => <li key={w.key}><IssueLink issueKey={w.key} /> waits: {w.reason}</li>)}
        </ul>
      )}
      {reply.overlaps.length > 0 && (
        <div>
          <p className="text-warn">Likely overlap with work in flight (a warning; nothing is held back):</p>
          <ul className="list-disc pl-5">
            {reply.overlaps.map((o) => (
              <li key={o.key + o.with}>
                <IssueLink issueKey={o.key} /> and <IssueLink issueKey={o.with} /> both touch <span className="mono text-xs [overflow-wrap:anywhere]">{o.paths.join(", ")}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Alert>
  );
}
