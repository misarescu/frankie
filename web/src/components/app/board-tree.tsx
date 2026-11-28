import { ChevronDownIcon, ChevronRightIcon, GitPullRequestIcon } from "lucide-react";
import { Fragment } from "react";
import { AppLink } from "@/components/app/AppLink";
import { Cost } from "@/components/app/Cost";
import { IssueLink } from "@/components/app/IssueLink";
import { IssueProgress } from "@/components/app/IssueProgress";
import { StatusBadge } from "@/components/app/StatusBadge";
import { TypeIcon } from "@/components/app/TypeIcon";
import { Checkbox } from "@/components/ui/checkbox";
import { waitsFor, type BoardRow, type Inbox } from "@/board";
import { isFinished, leafStatuses, type IssueSummary } from "@/issues";
import { cn } from "@/lib/utils";
import { paths } from "@/paths";

const th = "px-2 py-2 font-medium whitespace-nowrap";
// At phone width a row is a card: the issue on its first line, the rest
// wrapping beneath it.
const cell = "px-2 py-2 align-middle max-md:p-0";

// BoardTree is the board's list view: the issue hierarchy as a table whose
// parents fold, each row with what the issue waits for, its cost, change
// request and latest run, and a checkbox to start it.
export function BoardTree({ rows, issues, inbox, selected, onSelect, collapsed, onCollapse }: {
  rows: BoardRow[];
  issues: IssueSummary[];
  inbox: Inbox | undefined;
  selected: ReadonlySet<string>;
  onSelect: (key: string, on: boolean) => void;
  collapsed: ReadonlySet<string>;
  onCollapse: (key: string) => void;
}) {
  const byKey = new Map(issues.map((i) => [i.key, i]));
  const parents = new Set(issues.flatMap((i) => (i.parent ? [i.parent] : [])));
  return (
    <table className="w-full text-[13px] [--indent:20px] max-md:block max-md:[--indent:12px]">
      <thead className="border-b border-line text-left text-xs2 tracking-wider text-muted-fg uppercase max-md:hidden">
        <tr>
          <th className="w-8 px-3 py-2"><span className="sr-only">Select</span></th>
          <th className={th}>Issue</th>
          <th className={th}>Status</th>
          <th className={th}>Waits for</th>
          <th className={cn(th, "text-right")}>Cost</th>
          <th className={th}>Change request</th>
          <th className={cn(th, "pr-3")}>Latest run</th>
        </tr>
      </thead>
      <tbody className="max-md:block">
        {rows.map(({ issue: i, depth, children }) => {
          const finished = isFinished(i.status);
          const parent = parents.has(i.key);
          const folded = collapsed.has(i.key);
          const waits = waitsFor(i, byKey, inbox);
          return (
            <tr
              key={i.key}
              data-state={selected.has(i.key) ? "selected" : undefined}
              className={cn(
                "border-b border-line last:border-0 hover:bg-hover data-[state=selected]:bg-primary-soft/60",
                "max-md:flex max-md:flex-wrap max-md:items-center max-md:gap-x-3 max-md:gap-y-1.5 max-md:px-3 max-md:py-2.5",
                finished && "text-muted-fg",
              )}
            >
              <td className="w-8 px-3 py-2 align-middle max-md:w-auto max-md:p-0">
                <Checkbox
                  aria-label={`select ${i.key}`}
                  checked={selected.has(i.key)}
                  disabled={finished}
                  onCheckedChange={(v) => onSelect(i.key, v === true)}
                />
              </td>
              <td className={cn(cell, "max-md:min-w-0 max-md:flex-1 max-md:basis-[calc(100%-1.75rem)]")}>
                <div className="flex min-w-0 items-start gap-1.5" style={{ paddingLeft: `calc(${depth} * var(--indent))` }}>
                  {children > 0 ? (
                    <button
                      type="button"
                      aria-label={`${folded ? "expand" : "collapse"} ${i.key}`}
                      aria-expanded={!folded}
                      onClick={() => onCollapse(i.key)}
                      className="mt-px shrink-0 cursor-pointer rounded p-0.5 text-muted-fg hover:bg-muted hover:text-fg"
                    >
                      {folded ? <ChevronRightIcon className="size-3.5" /> : <ChevronDownIcon className="size-3.5" />}
                    </button>
                  ) : (
                    <span className="w-[18px] shrink-0" />
                  )}
                  <span className="mt-[3px]"><TypeIcon type={i.type} /></span>
                  <AppLink href={paths.issue(i.key)} className="line-clamp-2 min-w-0 [overflow-wrap:anywhere] hover:underline" title={i.title}>
                    <span className="mono mr-1 text-xs text-muted-fg">{i.key}</span>{" "}
                    <span className={cn(parent && "font-medium", !finished && "text-fg")}>{i.title}</span>
                  </AppLink>
                  {i.start_requested_at && !finished && (
                    <span className="mt-px shrink-0 rounded bg-muted px-1.5 text-xs2 text-muted-fg" title="someone asked to start it">requested</span>
                  )}
                </div>
              </td>
              <td className={cn(cell, "max-md:ml-7")}>
                <div className="flex items-center gap-3 whitespace-nowrap">
                  <StatusBadge status={i.status} />
                  {parent && <IssueProgress statuses={leafStatuses(issues, i.key)} />}
                </div>
              </td>
              <td className={cn(cell, "max-md:empty:hidden")}>
                {waits.length > 0 && (
                  <span className="text-xs [overflow-wrap:anywhere]">
                    <span className="text-muted-fg md:hidden">waits for </span>
                    {waits.map((w, n) => (
                      <Fragment key={w.kind === "issue" ? w.key : w.text}>
                        {n > 0 && ", "}
                        {w.kind === "issue" ? (
                          <IssueLink issueKey={w.key} />
                        ) : w.external ? (
                          <a href={w.href} target="_blank" rel="noreferrer" className="whitespace-nowrap text-warn hover:underline">{w.text}</a>
                        ) : (
                          <AppLink href={w.href} className="whitespace-nowrap text-warn hover:underline">{w.text}</AppLink>
                        )}
                      </Fragment>
                    ))}
                  </span>
                )}
              </td>
              <td className={cn(cell, "text-right whitespace-nowrap max-md:text-left", !parent && i.cost_usd <= 0 && "max-md:hidden")}>
                <Cost own={i.cost_usd} total={i.total_cost_usd} parent={parent} />
              </td>
              <td className={cn(cell, "max-md:empty:hidden")}>
                {i.change_request && (
                  <a
                    href={i.change_request.url}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-1 text-xs whitespace-nowrap hover:underline"
                    title={`change request #${i.change_request.number}, ${i.change_request.status}`}
                  >
                    <GitPullRequestIcon className={cn("size-3.5", i.change_request.status === "merged" ? "text-ok" : i.change_request.status === "open" ? "text-primary" : "text-muted-fg")} />
                    #{i.change_request.number}
                  </a>
                )}
              </td>
              <td className={cn(cell, "pr-3 max-md:empty:hidden max-md:pr-0")}>
                {i.latest_run && (
                  <AppLink href={paths.run(i.latest_run.id)} className="inline-flex hover:opacity-80" title="open its latest run">
                    <StatusBadge status={i.latest_run.status} />
                  </AppLink>
                )}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
