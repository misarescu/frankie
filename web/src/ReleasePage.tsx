import { useCallback, useEffect, useState } from "react";
import { StatusChip } from "./IssuesPage";
import {
  isLive, notesSourceText, releaseName, shortSHA, verificationState, verificationText, versionText,
  type Deployment, type ReleaseDetail,
} from "./releases";
import { usd } from "./steps";

// One release (D130): its version and notes (D129), each stage's deployment
// with how its verification ended and the failed tests, the issues it
// contains, the bug issue a failed verification opened, and the release
// agent's runs.
export function ReleasePage({ releaseID }: { releaseID: string }) {
  const [rel, setRelease] = useState<ReleaseDetail | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    fetch(`/api/releases/${releaseID}`)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(r.status === 404 ? "no such release" : `status ${r.status}`))))
      .then((d: ReleaseDetail) => { setRelease(d); setError(null); })
      .catch((e: Error) => setError(e.message));
  }, [releaseID]);
  useEffect(() => setRelease(null), [releaseID]);
  const live = rel ? isLive(rel.status) : true;
  useEffect(() => {
    load();
    if (!live) return;
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [load, live]);

  const head = <h1><a href="#/releases">Releases</a> / {rel ? releaseName(rel) : releaseID}</h1>;
  if (!rel) return <>{head}{error && <p className="status">{error}</p>}</>;
  return (
    <>
      {head}
      <h2>
        {releaseName(rel)} <span className="type" title={rel.sha}>· {rel.repo}@{shortSHA(rel.sha)}</span> <StatusChip status={rel.status} />
      </h2>
      <p className="status">
        {rel.project}
        {" · "}{versionText(rel)}
        {rel.bump && <> · {rel.bump} bump</>}
        {rel.provider_release_url && <> · <a href={rel.provider_release_url} target="_blank" rel="noreferrer">provider release</a></>}
        {rel.cost_usd > 0 && <> · release agent {usd(rel.cost_usd)}</>}
        {" · "}created {new Date(rel.created_at).toLocaleString()}
        {rel.finished_at && <>, finished {new Date(rel.finished_at).toLocaleString()}</>}
      </p>
      {rel.reason && <p className="question">{rel.reason}</p>}
      {rel.bug_issue_ref && (
        <p className="question">
          Its verification failed: <a href={rel.bug_issue_ref} target="_blank" rel="noreferrer">bug issue</a>
        </p>
      )}

      <section>
        <h3>Stages</h3>
        {rel.deployments.length === 0 && <p className="status">No stage deployed yet.</p>}
        {rel.deployments.map((d) => <DeploymentPanel key={d.stage} d={d} />)}
      </section>

      <section>
        <h3>Notes</h3>
        {rel.notes ? (
          <>
            <div className="spec-text notes">{rel.notes}</div>
            <p className="type">{notesSourceText(rel.notes_source)}</p>
          </>
        ) : (
          <p className="status">{isLive(rel.status) ? "Not written yet." : "None."}</p>
        )}
      </section>

      <section>
        <h3>Issues</h3>
        {rel.issue_details.length === 0 && <p className="status">None.</p>}
        <ul className="plain">
          {rel.issue_details.map((i) => (
            <li key={i.key}>
              <a href={`#/issues/${i.key}`}>{i.key}</a> {i.title} <span className="type">· {i.type}</span> <StatusChip status={i.status} />
            </li>
          ))}
        </ul>
      </section>

      {rel.runs.length > 0 && (
        <section>
          <h3>Release agent</h3>
          <table>
            <thead><tr><th>Run</th><th>Steps</th><th>Status</th><th>Cost</th></tr></thead>
            <tbody>
              {rel.runs.map((r) => (
                <tr key={r.id}>
                  <td><a href={`#/runs/${r.id}`}>{new Date(r.created_at).toLocaleString()}</a></td>
                  <td>{r.steps.join(", ")}</td>
                  <td><StatusChip status={r.status} /></td>
                  <td>{usd(r.cost_usd)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
    </>
  );
}

// DeploymentPanel is one stage: its outcome, why it failed, and how its
// verification ended with the failed tests and the issues they are tagged with.
function DeploymentPanel({ d }: { d: Deployment }) {
  const v = d.verification;
  return (
    <div className="approval">
      <p>
        <strong>{d.stage}</strong> <span className="type">· {d.driver}</span> <StatusChip status={d.status} />
        {d.finished_at && <span className="type"> · {new Date(d.finished_at).toLocaleString()}</span>}
      </p>
      {d.reason && <p className="question">{d.reason}</p>}
      {v && (
        <p>
          <span className={`chip st-${verificationState(v)}`}>verification</span> {verificationText(v)}
        </p>
      )}
      {v?.failures && v.failures.length > 0 && (
        <ul className="failures">
          {v.failures.map((f, n) => (
            <li key={n}>
              <code>{f.test}</code>
              {f.issueKeys && f.issueKeys.length > 0 && (
                <> <span className="type">· {f.issueKeys.map((k, m) => <span key={k}>{m > 0 && ", "}<a href={`#/issues/${k}`}>{k}</a></span>)}</span></>
              )}
              {f.message && <pre>{f.message}</pre>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
