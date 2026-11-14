import { useCallback, useEffect, useState } from "react";
import { decide, expiresIn, type Approval, type Decision } from "./approvals";
import { answer, type Question } from "./questions";
import { apiFetch } from "./auth";

const getJSON = <T,>(url: string): Promise<T> =>
  fetch(url).then((r) => (r.ok ? r.json() : Promise.reject(new Error(`status ${r.status}`))));

// The approvals inbox: questions agents asked with the ask-human tool (D55),
// then human gates waiting for a decision (docs/spec/03). An agent waits for
// its answer, so questions come first. Read the change on the provider before
// deciding; a decision goes to the run's workflow, which records it and
// continues or fails the run.
export function ApprovalsPage() {
  const [approvals, setApprovals] = useState<Approval[] | null>(null);
  const [questions, setQuestions] = useState<Question[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    Promise.all([getJSON<Question[]>("/api/questions"), getJSON<Approval[]>("/api/approvals")])
      .then(([qs, as]) => { setQuestions(qs); setApprovals(as); setError(null); })
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
      {questions?.map((q) => <QuestionCard key={q.id} question={q} onAnswered={load} />)}
      {approvals?.length === 0 && questions?.length === 0 && <p className="status">Nothing waits for an answer or a decision.</p>}
      {approvals?.map((a) => <ApprovalCard key={a.step_run_id} approval={a} onDecided={load} />)}
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
