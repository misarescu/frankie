import { activity } from "@/activity";
import { AppLink } from "@/components/app/AppLink";
import { Card } from "@/components/ui/card";
import type { IssueDetail } from "@/issues";
import { cn } from "@/lib/utils";
import { paths } from "@/paths";

const dot = { ok: "bg-ok", bad: "bg-bad", warn: "bg-warn" };

const when = (at: string) => new Date(at).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });

// IssueActivity is the Activity tab: what happened to the issue, oldest
// first, from its own fields (activity.ts).
export function IssueActivity({ issue }: { issue: IssueDetail }) {
  const entries = activity(issue);
  return (
    <Card>
      <ol aria-label="Activity" className="px-4 py-3">
        {entries.map((a, n) => (
          <li key={n} className="flex gap-4 border-l border-line pb-4 pl-4 last:pb-0">
            <span aria-hidden="true" className={cn("-ml-[21px] mt-1.5 size-2.5 shrink-0 rounded-full border-2 border-card", a.tone ? dot[a.tone] : "bg-muted-fg")} />
            <time dateTime={a.at} title={new Date(a.at).toLocaleString()} className="tnum w-28 shrink-0 text-xs text-muted-fg">{when(a.at)}</time>
            <span className="min-w-0 text-[13px] [overflow-wrap:anywhere]">
              {a.who && (a.run
                ? <AppLink href={paths.run(a.run)} className="font-medium text-primary hover:underline">{a.who}</AppLink>
                : <span className="font-medium">{a.who}</span>)}
              {a.who ? " " : ""}{a.what}
            </span>
          </li>
        ))}
      </ol>
    </Card>
  );
}
