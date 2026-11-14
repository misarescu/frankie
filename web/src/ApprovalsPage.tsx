import { useCallback, useEffect, useState } from "react";
import { decide, expiresIn, type Approval, type Decision } from "./approvals";
import { answer, type Question } from "./questions";
import { apiFetch } from "./auth";
import { decideSpec, type Spec, type SpecDecision } from "./specs";

const getJSON = <T,>(url: string): Promise<T> =>
  fetch(url).then((r) => (r.ok ? r.json() : Promise.reject(new Error(`status ${r.status}`))));

// The approvals inbox: questions agents asked with the ask-human tool (D55),
// then human gates waiting for a decision (docs/spec/03), then specs waiting
// for approval before their issue runs (D64). An agent waits for its answer,
// so questions come first. Read the change on the provider before
// deciding; a decision goes to the run's workflow, which records it and
// continues or fails the run.
export function ApprovalsPage() {
  const [approvals, setApprovals] = useState<Approval[] | null>(null);
  const [questions, setQuestions] = useState<Question[] | null>(null);
  const [specs, setSpecs] = useState<Spec[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    Promise.all([getJSON<Question[]>("/api/questions"), getJSON<Approval[]>("/api/approvals"), getJSON<Spec[]>("/api/specs")])
      .then(([qs, as, ss]) => { setQuestions(qs); setApprovals(as); setSpecs(ss); setError(null); })
      .catch((e: Error) => setError(e.message));
  }, []);
  useEffect(() => {
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [load]);

  return (
    <>
      <h1>Approvals <span className="type">· <a href="#/">Runs</a> · <a href="#/issues">Issues</a></span></h1>
      {error && <p className="status">Could not load approvals: {error}</p>}
      {questions?.map((q) => <QuestionCard key={q.id} question={q} onAnswered={load} />)}
      {approvals?.length === 0 && questions?.length === 0 && specs?.length === 0 && <p className="status">Nothing waits for an answer or a decision.</p>}
      {approvals?.map((a) => <ApprovalCard key={a.step_run_id} approval={a} onDecided={load} />)}
      {specs?.map((s) => <SpecCard key={s.key} spec={s} onDecided={load} />)}
    </>
  );
}

function QuestionCard({ question: q, onAnswered }: { question: Question; onAnswered: () => void }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const send = () => {
    setBusy(true);
    setError(null);
    answer(apiFetch, q.id, text)
      // The workflow records the answer once the runner took it.
      .then(() => setTimeout(onAnswered, 1500))
      .catch((e: Error) => { setError(e.message); setBusy(false); });
  };
  return (
    <section className="approval">
      <h2>
        <a href={`#/runs/${q.run_id}`}>{q.issue_key}</a> {q.issue_title} <span className="type">· {q.step} asks</span>
      </h2>
      <p className="question">{q.question}</p>
      <p className="status">waiting since {new Date(q.created_at).toLocaleString()}; the agent is paused until you answer</p>
      <textarea placeholder="Your answer (the agent gets it as the tool's result)" value={text} maxLength={8000}
        onChange={(e) => setText(e.target.value)} disabled={busy} />
      <div className="actions">
        <button onClick={send} disabled={busy || text.trim() === ""}>Answer</button>
        {busy && !error && <span className="status">sent…</span>}
        {error && <span className="status">{error}</span>}
      </div>
    </section>
  );
}

function ApprovalCard({ approval: a, onDecided }: { approval: Approval; onDecided: () => void }) {
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const send = (d: Decision) => {
    setBusy(true);
    setError(null);
    decide(apiFetch, a.step_run_id, d, comment)
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

function SpecCard({ spec: s, onDecided }: { spec: Spec; onDecided: () => void }) {
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const send = (d: SpecDecision) => {
    setBusy(true);
    setError(null);
    decideSpec(apiFetch, s.key, d, comment)
      .then(onDecided)
      .catch((e: Error) => { setError(e.message); setBusy(false); });
  };
  return (
    <section className="approval">
      <h2>
        {s.key} {s.title} <span className="type">· {s.type} spec</span>
      </h2>
      <p className="status">
        {s.project}
        {s.source_ref && <> · <a href={s.source_ref} target="_blank" rel="noreferrer">source</a></>}
        {s.run_id && <> · <a href={`#/runs/${s.run_id}`}>refinement</a></>}
        {s.requested_by && <> · started by {s.requested_by}</>}
        {" "}· runs once approved and its dependencies are merged
      </p>
      {s.summary && <p className="question">{s.summary}</p>}
      <pre>{s.spec || "(no description)"}</pre>
      {s.acceptance_criteria.length > 0 && (
        <ul>{s.acceptance_criteria.map((c, i) => <li key={i}>{c}</li>)}</ul>
      )}
      {s.file_hints.length > 0 && <p className="type">touches {s.file_hints.join(", ")}</p>}
      <details>
        <summary className="type">original issue</summary>
        <pre>{s.original_spec || "(no description)"}</pre>
      </details>
      <textarea placeholder="Comment (required to reject: what the refiner should change)" value={comment} maxLength={4000}
        onChange={(e) => setComment(e.target.value)} disabled={busy} />
      <div className="actions">
        <button onClick={() => send("approve")} disabled={busy}>Approve spec</button>
        <button className="reject" onClick={() => send("reject")} disabled={busy || comment.trim() === ""}>Reject</button>
        {busy && !error && <span className="status">sent…</span>}
        {error && <span className="status">{error}</span>}
      </div>
    </section>
  );
}
