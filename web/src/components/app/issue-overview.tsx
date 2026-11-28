import { AlertTriangleIcon, ExternalLinkIcon } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { SpecDecision } from "@/components/app/decisions";
import { IssueChildrenShort } from "@/components/app/issue-children";
import { LiveRunCard } from "@/components/app/issue-live-run";
import { IssueLink } from "@/components/app/IssueLink";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { specDecidable, type IssueDetail, type IssueRef } from "@/issues";
import { latestRun, watch, type LiveRunRef } from "@/live";

// useWatched is the runs the Overview follows (M5.1): an issue that does the
// work its latest run, finished or not; a parent the runs in progress under
// it. A run that finishes while the page is open stays with its outcome.
function useWatched(issue: IssueDetail): LiveRunRef[] {
  const latest = latestRun(issue);
  const incoming = issue.children.length > 0 ? issue.live_runs : [...issue.live_runs, ...(latest ? [latest] : [])];
  const [watched, setWatched] = useState(() => watch([], incoming));
  const sig = JSON.stringify(incoming);
  useEffect(() => setWatched((w) => watch(w, JSON.parse(sig) as LiveRunRef[])), [sig]);
  return watched;
}

function Section({ title, sub, children, footer }: { title: string; sub?: ReactNode; children: ReactNode; footer?: ReactNode }) {
  return (
    <Card>
      <CardHeader className="flex-wrap">
        <CardTitle>{title}</CardTitle>
        {sub && <CardDescription>{sub}</CardDescription>}
      </CardHeader>
      {children}
      {footer && <CardFooter>{footer}</CardFooter>}
    </Card>
  );
}

function Refs({ refs }: { refs: IssueRef[] }) {
  if (refs.length === 0) return <span className="text-muted-fg">nothing</span>;
  return (
    <ul className="space-y-1">
      {refs.map((r) => (
        <li key={r.key} className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <IssueLink issueKey={r.key} title={r.title} className="[overflow-wrap:anywhere]" />
          <StatusBadge status={r.status} />
        </li>
      ))}
    </ul>
  );
}

function Paths({ paths }: { paths: string[] }) {
  return (
    <ul className="space-y-1 px-4 py-3">
      {paths.map((p) => <li key={p} className="mono text-xs [overflow-wrap:anywhere]">{p}</li>)}
    </ul>
  );
}

const at = (t: string) => new Date(t).toLocaleString();

// IssueOverview is the Overview tab: the live run card, the spec and its
// acceptance criteria, the issues under a parent, and beside them relations,
// overlaps, change requests, changed paths, file hints and details.
export function IssueOverview({ issue }: { issue: IssueDetail }) {
  const watched = useWatched(issue);
  const parent = issue.children.length > 0;
  const draft = specDecidable(issue) && issue.status === "draft";
  return (
    <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_300px]">
      <div className="min-w-0 space-y-5">
        {watched.map((r) => <LiveRunCard key={r.id} run={r} showIssue={r.issue_key !== issue.key} />)}
        <Section
          title="Spec"
          sub={issue.spec_approved_by ? `approved by ${issue.spec_approved_by}` : issue.source_ref ? "from the source issue" : undefined}
          footer={draft && (
            <div className="space-y-2">
              <p className="text-xs text-muted-fg">
                {issue.spec_rejected_at ? "Held: approve the spec as written, or ask again (Refine again) to have it refined." : "Approve the spec as written, or send it back with what to change."}
              </p>
              <SpecDecision key={issue.spec_rejected_at ?? "draft"} issueKey={issue.key} />
            </div>
          )}
        >
          <CardContent className="space-y-3">
            {issue.spec_feedback && (
              <p className="rounded-md bg-warn-soft px-3 py-2 text-[13px] [overflow-wrap:anywhere]">
                <span className="font-medium">{issue.spec_rejected_at ? "Held" : "Last sent back"}:</span> {issue.spec_feedback}
              </p>
            )}
            {issue.spec
              ? <div className="max-w-[72ch] text-[13.5px] leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]">{issue.spec}</div>
              : <p className="text-[13px] text-muted-fg">No description.</p>}
          </CardContent>
        </Section>
        {issue.acceptance_criteria.length > 0 && (
          <Section title="Acceptance criteria" sub="the tests are derived from these">
            <ol className="divide-y divide-line">
              {issue.acceptance_criteria.map((c, n) => (
                <li key={n} className="flex gap-3 px-4 py-2 text-[13px]">
                  <span className="tnum mono w-4 shrink-0 text-muted-fg">{n + 1}</span>
                  <span className="min-w-0 [overflow-wrap:anywhere]">{c}</span>
                </li>
              ))}
            </ol>
          </Section>
        )}
        {parent && <IssueChildrenShort issue={issue} />}
      </div>
      <aside className="min-w-0 space-y-5">
        <Section title="Relations">
          <dl className="space-y-2.5 px-4 py-3 text-[13px]">
            {issue.parent && <div><dt className="text-xs text-muted-fg">Part of</dt><dd><Refs refs={[issue.parent]} /></dd></div>}
            <div><dt className="text-xs text-muted-fg">Waits for</dt><dd><Refs refs={issue.depends_on} /></dd></div>
            <div><dt className="text-xs text-muted-fg">Needed by</dt><dd><Refs refs={issue.dependents} /></dd></div>
          </dl>
        </Section>
        {issue.overlaps.length > 0 && (
          <section aria-label="Overlaps" className="rounded-lg border border-warn/40 bg-warn-soft px-4 py-3 text-[13px]">
            <h3 className="flex items-center gap-2 font-medium text-warn"><AlertTriangleIcon className="size-3.5" aria-hidden="true" />Overlaps with in-flight work</h3>
            {issue.overlaps.map((o) => (
              <p key={o.key + o.with} className="mt-1 text-muted-fg [overflow-wrap:anywhere]">
                <IssueLink issueKey={o.with} /> also touches <span className="mono text-xs">{o.paths.join(", ")}</span>
              </p>
            ))}
          </section>
        )}
        {issue.change_requests.length > 0 && (
          <Section title="Change requests">
            <ul className="space-y-1.5 px-4 py-3 text-[13px]">
              {[...issue.change_requests].reverse().map((c) => (
                <li key={c.url} className="flex flex-wrap items-center gap-2">
                  <a href={c.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-primary hover:underline [overflow-wrap:anywhere]">
                    {c.repo}#{c.number}<ExternalLinkIcon className="size-3 shrink-0" aria-hidden="true" />
                  </a>
                  <StatusBadge status={c.status} />
                </li>
              ))}
            </ul>
          </Section>
        )}
        {issue.changed_paths.length > 0 && (
          <Section title="Changed paths" sub="on its branch"><Paths paths={issue.changed_paths} /></Section>
        )}
        {issue.file_hints.length > 0 && (
          <Section title="File hints" sub="where the spec says to look"><Paths paths={issue.file_hints} /></Section>
        )}
        <Section title="Details">
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 px-4 py-3 text-[13px] [&_dd]:[overflow-wrap:anywhere]">
            <dt className="text-muted-fg">Project</dt><dd>{issue.project}</dd>
            <dt className="text-muted-fg">Priority</dt><dd>P{issue.priority}</dd>
            {issue.labels.length > 0 && (
              <>
                <dt className="text-muted-fg">Labels</dt>
                <dd className="flex flex-wrap gap-1">{issue.labels.map((l) => <span key={l} className="rounded bg-muted px-1.5 text-xs2">{l}</span>)}</dd>
              </>
            )}
            <dt className="text-muted-fg">Created</dt><dd>{at(issue.created_at)}{issue.created_by && <span className="text-muted-fg"> by {issue.created_by}</span>}</dd>
            <dt className="text-muted-fg">Start request</dt>
            <dd>
              {issue.start_requested_by
                ? <>{issue.start_requested_by}{issue.start_requested_via && <> through <IssueLink issueKey={issue.start_requested_via} /></>}
                  {issue.start_requested_at && <span className="text-muted-fg">, {at(issue.start_requested_at)}</span>}</>
                : <span className="text-muted-fg">none</span>}
            </dd>
            {(issue.spec_approved_by || issue.spec_rejected_at) && (
              <>
                <dt className="text-muted-fg">Spec</dt>
                <dd>
                  {issue.spec_approved_by
                    ? <>approved by {issue.spec_approved_by}{issue.spec_approved_at && <span className="text-muted-fg">, {at(issue.spec_approved_at)}</span>}</>
                    : <>held since {at(issue.spec_rejected_at!)}</>}
                </dd>
              </>
            )}
            {issue.closed_at && (
              <>
                <dt className="text-muted-fg">Closed</dt>
                <dd>{at(issue.closed_at)}{issue.closed_by && <span className="text-muted-fg"> by {issue.closed_by}</span>}</dd>
              </>
            )}
            <dt className="text-muted-fg">Tokens</dt>
            <dd className="tnum">{(issue.usage.input_tokens + issue.usage.output_tokens).toLocaleString("en-US")}</dd>
          </dl>
        </Section>
      </aside>
    </div>
  );
}
