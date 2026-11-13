import { useCallback, useEffect, useState } from "react";
import { decide, expiresIn, type Approval, type Decision } from "./approvals";

// The approvals inbox: human gates waiting for a decision (docs/spec/03).
// Read the change on the provider first; a decision goes to the run's
// workflow, which records it and continues or fails the run.
export function ApprovalsPage() {
  const [approvals, setApprovals] = useState<Approval[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    fetch("/api/approvals")
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`status ${r.status}`))))
      .then((data: Approval[]) => { setApprovals(data); setError(null); })
      .catch((e: Error) => setError(e.message));
  }, []);
  useEffect(() => {
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [load]);

  return (
    <>
      <h1><a href="#/">Runs</a> / Approvals</h1>
      {error && <p className="status">Could not load approvals: {error}</p>}
      {approvals?.length === 0 && <p className="status">Nothing waits for a decision.</p>}
      {approvals?.map((a) => <ApprovalCard key={a.step_run_id} approval={a} onDecided={load} />)}
    </>
  );
}

function ApprovalCard({ approval: a, onDecided }: { approval: Approval; onDecided: () => void }) {
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const send = (d: Decision) => {
    setBusy(true);
    setError(null);
    decide(fetch, a.step_run_id, d, comment)
      // The workflow records the decision right after the signal.
      .then(() => setTimeout(onDecided, 1500))
      .catch((e: Error) => { setError(e.message); setBusy(false); });
  };
  return (
    <section className="approval">
      <h2>
        <a href={`#/runs/${a.run_id}`}>{a.issue_key}</a> {a.issue_title} <span className="type">· {a.step}</span>
      </h2>
      <p>{a.reason}</p>
      <p className="status">
        {a.change_request_url && <><a href={a.change_request_url} target="_blank" rel="noreferrer">Review the change</a> · </>}
        {a.commit_sha && <>commit {a.commit_sha.slice(0, 12)} · </>}
        waiting since {new Date(a.created_at).toLocaleString()} · expires in {expiresIn(a.expires_at)}
      </p>
      <textarea placeholder="Comment (optional; recorded with the decision)" value={comment} maxLength={4000}
        onChange={(e) => setComment(e.target.value)} disabled={busy} />
      <div className="actions">
        <button onClick={() => send("approve")} disabled={busy}>Approve</button>
        <button className="reject" onClick={() => send("reject")} disabled={busy}>Reject</button>
        {busy && !error && <span className="status">sent…</span>}
        {error && <span className="status">{error}</span>}
      </div>
    </section>
  );
}
