import { useCallback, useEffect, useState } from "react";
import { apiFetch } from "./auth";
import {
  cancelIssue, completeIssue, issueActions, retryIssue, shortID, specDecidable, startIssues,
  type CloseReply, type IssueDetail, type IssueRef, type StartReply,
} from "./issues";
import { StartReplyPanel, StatusChip } from "./IssuesPage";
import { latestRun, watch, type LiveRunRef } from "./live";
import { LiveRun } from "./LiveRun";
import { ParentIssues } from "./ParentIssues";
import { decideSpec, type SpecDecision } from "./specs";
import { usd } from "./steps";

// The issue page (M5): spec and acceptance criteria, a refined spec waiting
// for approval, relations, runs and cost, overlap warnings, and the actions a
// person takes on the issue: start, retry, approve or reject its spec.
export function IssuePage({ issueKey }: { issueKey: string }) {
  const [is, setIssue] = useState<IssueDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reply, setReply] = useState<StartReply | null>(null);
  const [closed, setClosed] = useState<CloseReply | null>(null);
  const [canceling, setCanceling] = useState(false);
  const [busy, setBusy] = useState(false);
  // Runs the live view follows: the latest run of an issue that does the work,
  // those in progress under a parent, and those that finished while the page
  // was open (M5.1).
  const [watched, setWatched] = useState<LiveRunRef[]>([]);
  // Poll quickly while something runs or right after a start, so the first
  // run shows up within seconds of the scheduler starting it.
  const [fastUntil, setFastUntil] = useState(0);

  const load = useCallback(() => {
    fetch(`/api/issues/${encodeURIComponent(issueKey)}`)
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(r.status === 404 ? "no such issue" : `status ${r.status}`))))
      .then((d: IssueDetail) => {
        setIssue(d);
        setError(null);
        // An issue that does the work shows its runs: its latest, finished or
        // not, and any that finished while the page was open. A parent lists
        // its issues instead (ParentIssues).
        const latest = latestRun(d);
        setWatched((w) => (d.children.length > 0 ? [] : watch(w, [...(d.live_runs ?? []), ...(latest ? [latest] : [])])));
      })
      .catch((e: Error) => setError(e.message));
  }, [issueKey]);
  useEffect(() => {
    setIssue(null);
    setReply(null);
    setClosed(null);
    setCanceling(false);
    setWatched([]);
  }, [issueKey]);
  const fast = (is?.live_runs?.length ?? 0) > 0 || fastUntil > Date.now();
  useEffect(() => {
    load();
    const t = setInterval(() => {
      if (fastUntil !== 0 && fastUntil <= Date.now()) setFastUntil(0);
      load();
    }, fast ? 3000 : 10000);
    return () => clearInterval(t);
  }, [load, fast, fastUntil]);

  const act = (p: Promise<unknown>) => {
    setBusy(true);
    setError(null);
    p.then(load).catch((e: Error) => setError(e.message)).finally(() => setBusy(false));
  };
  const start = () => {
    setFastUntil(Date.now() + 60000);
    act(startIssues(apiFetch, [issueKey]).then(setReply));
  };

  const complete = () => {
    if (!is || !window.confirm(`Complete ${is.key}? It moves to merged, and Frankie closes its GitHub issue as completed.`)) return;
    act(completeIssue(apiFetch, is.key).then(setClosed));
  };

  const head = <h1><a href="#/issues">Issues</a> / {issueKey}</h1>;
  if (!is) return <>{head}{error && <p className="status">{error}</p>}</>;
  const actions = issueActions(is);
  return (
    <>
      {head}
      <h2>{is.title} <span className="type">· {is.type}</span> <StatusChip status={is.status} /></h2>
      <p className="status">
        {is.project}
        {is.source_ref && <> · <a href={is.source_ref} target="_blank" rel="noreferrer">source</a></>}
        {is.spec_approved_by && <> · spec approved by {is.spec_approved_by}</>}
        {is.usage.cost_usd > 0 && <> · {usd(is.usage.cost_usd)} ({is.usage.input_tokens + is.usage.output_tokens} tokens)</>}
      </p>
      {is.closed_at && (
        <p className="question">
          {is.status === "canceled" ? "Canceled" : "Completed"} by {is.closed_by || "someone"} on {new Date(is.closed_at).toLocaleString()}
          {is.close_reason && <>: {is.close_reason}</>}
        </p>
      )}

      <Relations title="part of" refs={is.parent ? [is.parent] : []} />
      {is.children.length > 0 && <ParentIssues project={is.project} issueKey={is.key} live={is.live_runs.length > 0} />}
      <Relations title="waits for" refs={is.depends_on} />
      <Relations title="needed by" refs={is.dependents} />

      <div className="actions">
        {actions.start && <button disabled={busy} onClick={start}>{actions.start}</button>}
        {actions.retry && <button disabled={busy} onClick={() => act(retryIssue(apiFetch, is.key))}>Retry</button>}
        {actions.complete && <button disabled={busy} onClick={complete}>Complete</button>}
        {actions.cancel && !canceling && <button className="reject" disabled={busy} onClick={() => setCanceling(true)}>Cancel…</button>}
        {actions.note && (
          <span className="status">
            {actions.note}
            {actions.changeRequest && <>: <a href={actions.changeRequest} target="_blank" rel="noreferrer">open it</a></>}
          </span>
        )}
        {error && <span className="status">{error}</span>}
      </div>
      {canceling && (
        <CancelForm issue={is} onDone={(r) => { setCanceling(false); setClosed(r); load(); }} onClose={() => setCanceling(false)} />
      )}
      {closed && (
        <p className="status">
          Closed: {closed.closed.map((c, n) => <span key={c.key}>{n > 0 && ", "}<a href={`#/issues/${c.key}`}>{c.key}</a> ({c.status})</span>)}.
          {" "}Frankie closes their GitHub issues within a minute.
        </p>
      )}
      {reply && <StartReplyPanel reply={reply} onClose={() => setReply(null)} />}
      {is.spec_feedback && (
        <p className="question">{is.spec_rejected_at ? "Held" : "Last spec sent back"}: {is.spec_feedback}</p>
      )}
      {is.overlaps.length > 0 && (
        <p className="question">
          Likely overlap: {is.overlaps.map((o) => `${o.with} (${o.paths.join(", ")})`).join("; ")}
        </p>
      )}
      {watched.map((r) => <LiveRun key={r.id} run={r} showIssue={r.issue_key !== is.key} />)}

      {is.spec_proposal && (
        <section className="approval">
          <h2>Refined spec waiting for approval</h2>
          {is.spec_proposal.summary && <p className="question">{is.spec_proposal.summary}</p>}
          <div className="spec-text">{is.spec_proposal.spec}</div>
          <Criteria items={is.spec_proposal.acceptance_criteria} />
          {(is.spec_proposal.file_hints?.length ?? 0) > 0 && <p className="type">touches {is.spec_proposal.file_hints!.join(", ")}</p>}
        </section>
      )}
      {specDecidable(is) && <SpecDecisionForm issueKey={is.key} refined={!!is.spec_proposal} onDecided={load} />}

      <section className="spec">
        <h3>Spec</h3>
        <div className="spec-text">{is.spec || <span className="type">(no description)</span>}</div>
        <Criteria items={is.acceptance_criteria} />
      </section>

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
      <h3 className="type">acceptance criteria</h3>
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

// CancelForm cancels the issue and everything unfinished under it, with a
// reason that is also posted on the GitHub issues (D70, D71).
function CancelForm({ issue, onDone, onClose }: { issue: IssueDetail; onDone: (r: CloseReply) => void; onClose: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const send = () => {
    setBusy(true);
    setError(null);
    cancelIssue(apiFetch, issue.key, reason)
      .then(onDone)
      .catch((e: Error) => setError(e.message))
      .finally(() => setBusy(false));
  };
  return (
    <section className="approval">
      <h2>Cancel {issue.key}</h2>
      <p className="type">
        {issue.children.length > 0 ? "It and every unfinished issue under it move" : "It moves"} to canceled, and Frankie closes
        {" "}the GitHub {issue.children.length > 0 ? "issues" : "issue"} as not planned, with this reason. A run in progress has to end first.
      </p>
      <textarea placeholder="Why is it canceled? (posted on GitHub)" value={reason} maxLength={2000}
        onChange={(e) => setReason(e.target.value)} disabled={busy} />
      <div className="actions">
        <button className="reject" onClick={send} disabled={busy || reason.trim() === ""}>Cancel issue</button>
        <button onClick={onClose} disabled={busy}>Keep it</button>
        {error && <span className="status">{error}</span>}
      </div>
    </section>
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
