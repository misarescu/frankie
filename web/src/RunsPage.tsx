import { useEffect, useState } from "react";
import type { RunSummary } from "./events";
import { usd } from "./steps";

export function RunsPage() {
  const [runs, setRuns] = useState<RunSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    fetch("/api/runs")
      .then((r) => (r.ok ? r.json() : Promise.reject(new Error(`status ${r.status}`))))
      .then((data: RunSummary[]) => setRuns(data))
      .catch((e: Error) => setError(e.message));
  }, []);
  return (
    <>
      <h1>Runs <span className="type">· <a href="#/approvals">Approvals</a></span></h1>
      {error && <p className="status">Could not load runs: {error}</p>}
      <table>
        <thead><tr><th>Issue</th><th>Title</th><th>Status</th><th>Cost</th><th>Created</th></tr></thead>
        <tbody>
          {runs?.length === 0 && <tr><td colSpan={5}>No runs yet.</td></tr>}
          {runs?.map((r) => (
            <tr key={r.ID}>
              <td><a href={`#/runs/${r.ID}`}>{r.IssueKey}</a></td>
              <td>{r.IssueTitle}</td>
              <td>{r.Status}</td>
              <td>{usd(r.CostUSD ?? 0)}</td>
              <td>{new Date(r.CreatedAt).toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
