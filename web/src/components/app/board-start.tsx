import { PlayIcon, XIcon } from "lucide-react";
import { IssueLink } from "@/components/app/IssueLink";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import type { StartReply } from "@/issues";

const Keys = ({ keys }: { keys: string[] }) => (
  <>{keys.map((k, n) => <span key={k}>{n > 0 && ", "}<IssueLink issueKey={k} /></span>)}</>
);

// StartReplyPanel shows the scheduler's reply to a start request (D62, D63):
// what was asked for, what starts on its next pass, what waits and why, and
// work in flight the started issues likely overlap.
export function StartReplyPanel({ reply, onDismiss }: { reply: StartReply; onDismiss: () => void }) {
  return (
    <Card role="status" className="border-primary/40 bg-primary-soft/40 px-4 py-3 text-[13px]">
      <div className="flex items-start gap-3">
        <PlayIcon className="mt-0.5 size-4 shrink-0 text-primary" />
        <div className="min-w-0 flex-1 space-y-1.5 [overflow-wrap:anywhere]">
          <p className="font-medium">Start requested: {reply.requested.join(", ")}</p>
          {reply.starting.length > 0 && <p>Starting on the next pass: <Keys keys={reply.starting} /></p>}
          {reply.waiting.length > 0 && (
            <div>
              <p className="text-muted-fg">Waiting:</p>
              <ul className="ml-4 list-disc">
                {reply.waiting.map((w) => <li key={w.key}><IssueLink issueKey={w.key} />: {w.reason}</li>)}
              </ul>
            </div>
          )}
          {reply.overlaps.length > 0 && (
            <div>
              <p className="text-warn">Likely overlap with work in flight (a warning; nothing is held back):</p>
              <ul className="ml-4 list-disc">
                {reply.overlaps.map((o) => (
                  <li key={o.key + o.with}>
                    <IssueLink issueKey={o.key} /> and <IssueLink issueKey={o.with} /> both touch <span className="mono text-xs">{o.paths.join(", ")}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
        <Button variant="ghost" size="icon-sm" onClick={onDismiss} aria-label="Dismiss">
          <XIcon className="size-4" />
        </Button>
      </div>
    </Card>
  );
}
