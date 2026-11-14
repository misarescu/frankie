import { useEffect, useState } from "react";
import { isFinished, leafStatuses, progress, progressText, subtree, type IssueSummary } from "./issues";
import { StatusChip } from "./IssuesPage";
import { usd } from "./steps";

// ParentIssues is what a parent's page shows (M5.1): every issue under it as
// a tree, each with its state, cost, change request and latest run, and the
// totals. The agents' work is on each issue's own page, one click away.
export function ParentIssues({ project, issueKey, live }: { project: string; issueKey: string; live: boolean }) {
  const [issues, setIssues] = useState<IssueSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () => fetch(`/api/issues?project=${encodeURIComponent(project)}`)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`status ${r.status}`))))
      .then((d: IssueSummary[]) => { if (alive) { setIssues(d); setError(null); } })
      .catch((e: Error) => { if (alive) setError(e.message); });
    load();
    const t = setInterval(load, live ? 3000 : 10000);
    return () => { alive = false; clearInterval(t); };
  }, [project, issueKey, live]);

  if (!issues) return error ? <p className="status">{error}</p> : null;
  const { rows, cost } = subtree(issues, issueKey);
  if (rows.length === 0) return null;
  const leaves = leafStatuses(issues, issueKey);
  return (
    <section className="children">
      <h3 className="type">
        contains {rows.length} {rows.length === 1 ? "issue" : "issues"} · {progressText(progress(leaves))} · total {usd(cost)}
      </h3>
      <table className="issues">
        <thead><tr><th>Issue</th><th>Status</th><th>Waits for</th><th>Cost</th><th>Change request</th><th>Run</th></tr></thead>
        <tbody>
          {rows.map(({ issue: i, depth }) => (
            <tr key={i.key} className={isFinished(i.status) ? "finished" : undefined}>
              <td style={{ paddingLeft: 8 + depth * 18 }}>
                <a href={`#/issues/${i.key}`}>{i.key}</a> {i.title} <span className="type">· {i.type}</span>
              </td>
              <td><StatusChip status={i.status} /></td>
              <td>{i.depends_on.map((d, n) => <span key={d}>{n > 0 && ", "}<a href={`#/issues/${d}`}>{d}</a></span>)}</td>
              <td>{i.cost_usd > 0 ? usd(i.cost_usd) : ""}</td>
              <td>
                {i.change_request && (
                  <><a href={i.change_request.url} target="_blank" rel="noreferrer">#{i.change_request.number}</a> <span className="type">{i.change_request.status}</span></>
                )}
              </td>
              <td>{i.latest_run && <a href={`#/runs/${i.latest_run.id}`}>{i.latest_run.status}</a>}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
