import { useCallback, useEffect, useState } from "react";
import { apiFetch } from "./auth";
import { isFinished, retryIssue, shortID, startIssues, type IssueDetail, type IssueRef, type StartReply } from "./issues";
import { StartReplyPanel, StatusChip } from "./IssuesPage";
import { decideSpec, type SpecDecision } from "./specs";
import { usd } from "./steps";

// The issue page (M5): spec and acceptance criteria, a refined spec waiting
// for approval, relations, runs and cost, overlap warnings, and the actions a
// person takes on the issue: start, retry, approve or reject its spec.
export function IssuePage({ issueKey }: { issueKey: string }) {
  const [is, setIssue] = useState<IssueDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reply, setReply] = useState<StartReply | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    fetch(`/api/issues/${encodeURIComponent(issueKey)}`)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(r.status === 404 ? "no such issue" : `status ${r.status}`))))
      .then((d: IssueDetail) => { setIssue(d); setError(null); })
      .catch((e: Error) => setError(e.message));
  }, [issueKey]);
  useEffect(() => {
    setIssue(null);
    setReply(null);
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [load]);

  const act = (p: Promise<unknown>) => {
    setBusy(true);
    setError(null);
    p.then(load).catch((e: Error) => setError(e.message)).finally(() => setBusy(false));
  };

  const head = <h1><a href="#/issues">Issues</a> / {issueKey}</h1>;
  if (!is) return <>{head}{error && <p className="status">{error}</p>}</>;
  const decidable = (is.status === "refining" && is.spec_proposal) || is.status === "draft";
  return (
    <>
      {head}
      <h2>{is.title} <span className="type">· {is.type}</span> <StatusChip status={is.status} /></h2>
      <p className="status">
        {is.project}
        {is.source_ref && <> · <a href={is.source_ref} target="_blank" rel="noreferrer">source</a></>}
        {is.start_requested_by && <> · start requested by {is.start_requested_by}</>}
        {is.spec_approved_by && <> · spec approved by {is.spec_approved_by}</>}
        {is.usage.cost_usd > 0 && <> · {usd(is.usage.cost_usd)} ({is.usage.input_tokens + is.usage.output_tokens} tokens)</>}
      </p>
      <div className="actions">
        {!isFinished(is.status) && (
          <button disabled={busy} onClick={() => act(startIssues(apiFetch, [is.key]).then(setReply))}>
            {is.children.length > 0 ? "Start all under it" : "Start"}
          </button>
        )}
        {is.status === "failed" && <button disabled={busy} onClick={() => act(retryIssue(apiFetch, is.key))}>Retry</button>}
        {error && <span className="status">{error}</span>}
      </div>
      {reply && <StartReplyPanel reply={reply} onClose={() => setReply(null)} />}
      {is.spec_feedback && <p className="question">Last spec sent back: {is.spec_feedback}</p>}
      {is.overlaps.length > 0 && (
        <p className="question">
          Likely overlap: {is.overlaps.map((o) => `${o.with} (${o.paths.join(", ")})`).join("; ")}
        </p>
      )}

      {is.spec_proposal && (
        <section className="approval">
          <h2>Refined spec waiting for approval</h2>
          {is.spec_proposal.summary && <p className="question">{is.spec_proposal.summary}</p>}
          <pre>{is.spec_proposal.spec}</pre>
          <Criteria items={is.spec_proposal.acceptance_criteria} />
          {(is.spec_proposal.file_hints?.length ?? 0) > 0 && <p className="type">touches {is.spec_proposal.file_hints!.join(", ")}</p>}
        </section>
      )}
      {decidable && <SpecDecisionForm issueKey={is.key} refined={!!is.spec_proposal} onDecided={load} />}

      <h2 className="type">spec</h2>
      <pre>{is.spec || "(no description)"}</pre>
      <Criteria items={is.acceptance_criteria} />

      <Relations title="part of" refs={is.parent ? [is.parent] : []} />
      <Relations title="contains" refs={is.children} />
      <Relations title="waits for" refs={is.depends_on} />
      <Relations title="needed by" refs={is.dependents} />

      {is.runs.length > 0 && (
        <>
          <h2 className="type">runs</h2>
          <table>
            <tbody>
              {is.runs.map((r) => (
                <tr key={r.id}>
                  <td><a href={`#/runs/${r.id}`} title={r.id}>{shortID(r.id)}</a></td>
                  <td>{new Date(r.created_at).toLocaleString()}</td>
                  <td className="type">{r.workflow}</td>
                  <td>{r.status}</td>
                  <td>{usd(r.cost_usd)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      {is.change_requests.length > 0 && (
        <p className="status">
          Change requests: {is.change_requests.map((c, n) => (
            <span key={c.url}>{n > 0 && ", "}<a href={c.url} target="_blank" rel="noreferrer">{c.repo}#{c.number}</a> ({c.status})</span>
          ))}
        </p>
      )}
      {(is.file_hints.length > 0 || is.changed_paths.length > 0) && (
        <p className="type">
          {is.file_hints.length > 0 && <>hints: {is.file_hints.join(", ")}</>}
          {is.file_hints.length > 0 && is.changed_paths.length > 0 && " · "}
          {is.changed_paths.length > 0 && <>changed: {is.changed_paths.join(", ")}</>}
        </p>
      )}
    </>
  );
}

function Criteria({ items }: { items: string[] }) {
  if (items.length === 0) return null;
  return (
    <>
      <h2 className="type">acceptance criteria</h2>
      <ul>{items.map((c, i) => <li key={i}>{c}</li>)}</ul>
    </>
  );
}

function Relations({ title, refs }: { title: string; refs: IssueRef[] }) {
  if (refs.length === 0) return null;
  return (
    <p>
      <span className="type">{title}: </span>
      {refs.map((r, n) => (
        <span key={r.key}>{n > 0 && " · "}<a href={`#/issues/${r.key}`}>{r.key}</a> {r.title} <StatusChip status={r.status} /></span>
      ))}
    </p>
  );
}

// SpecDecisionForm approves or rejects the refined spec, or a draft's text as
// written (D64, D66).
function SpecDecisionForm({ issueKey, refined, onDecided }: { issueKey: string; refined: boolean; onDecided: () => void }) {
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const send = (d: SpecDecision) => {
    setBusy(true);
    setError(null);
    decideSpec(apiFetch, issueKey, d, comment)
      .then(() => { setComment(""); onDecided(); })
      .catch((e: Error) => setError(e.message))
      .finally(() => setBusy(false));
  };
  return (
    <>
      <textarea placeholder="Comment (required to reject: what should change)" value={comment} maxLength={4000}
        onChange={(e) => setComment(e.target.value)} disabled={busy} />
      <div className="actions">
        <button onClick={() => send("approve")} disabled={busy}>{refined ? "Approve refined spec" : "Approve spec as written"}</button>
        <button className="reject" onClick={() => send("reject")} disabled={busy || comment.trim() === ""}>Reject</button>
        {error && <span className="status">{error}</span>}
      </div>
    </>
  );
}
