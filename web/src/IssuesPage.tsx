import { useCallback, useEffect, useState } from "react";
import { apiFetch } from "./auth";
import {
  dependencyGraph, isFinished, leafStatuses, progress, progressText, startIssues, tree, type IssueSummary, type Project, type StartReply,
} from "./issues";
import { usd } from "./steps";

const getJSON = <T,>(url: string): Promise<T> =>
  fetch(url).then((r) => (r.ok ? r.json() : Promise.reject(new Error(`status ${r.status}`))));

// The project the board and the releases page show, remembered per browser.
const PROJECT_KEY = "frankie.project";
export const remembered = (): string | null => {
  try {
    return localStorage.getItem(PROJECT_KEY);
  } catch {
    return null;
  }
};
export const remember = (name: string) => {
  try {
    localStorage.setItem(PROJECT_KEY, name);
  } catch {
    // a blocked storage only forgets the choice
  }
};

// The board (M5): a project's issues as their hierarchy, with what each waits
// for, and the dependency graph. Selected issues start together; a parent covers
// everything under it, and the scheduler runs them in dependency order (D62).
export function IssuesPage() {
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [project, setProject] = useState<string | null>(null);
  const [issues, setIssues] = useState<IssueSummary[] | null>(null);
  const [showFinished, setShowFinished] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [reply, setReply] = useState<StartReply | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    getJSON<Project[]>("/api/projects")
      .then((ps) => {
        setProjects(ps);
        const want = remembered();
        setProject(ps.find((p) => p.name === want)?.name ?? ps[0]?.name ?? null);
      })
      .catch((e: Error) => setError(e.message));
  }, []);

  const load = useCallback(() => {
    if (!project) return;
    getJSON<IssueSummary[]>(`/api/issues?project=${encodeURIComponent(project)}`)
      .then((is) => { setIssues(is); setError(null); })
      .catch((e: Error) => setError(e.message));
  }, [project]);
  useEffect(() => {
    setIssues(null);
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [load]);

  const toggle = (key: string) => setSelected((s) => {
    const next = new Set(s);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    return next;
  });
  const start = () => {
    setBusy(true);
    setError(null);
    startIssues(apiFetch, [...selected])
      .then((r) => { setReply(r); setSelected(new Set()); load(); })
      .catch((e: Error) => setError(e.message))
      .finally(() => setBusy(false));
  };

  const rows = issues ? tree(issues, (i) => !showFinished && isFinished(i.status)) : [];
  return (
    <>
      <h1>
        Issues <span className="type">· <a href="#/">Runs</a> · <a href="#/releases">Releases</a> · <a href="#/approvals">Approvals</a></span>
      </h1>
      <div className="actions">
        {projects && projects.length > 1 && (
          <select value={project ?? ""} onChange={(e) => { setProject(e.target.value); remember(e.target.value); setSelected(new Set()); setReply(null); }}>
            {projects.map((p) => <option key={p.id} value={p.name}>{p.name}</option>)}
          </select>
        )}
        {projects?.length === 1 && <span className="status">{project}</span>}
        <label className="type"><input type="checkbox" checked={showFinished} onChange={(e) => setShowFinished(e.target.checked)} /> show finished</label>
        <button onClick={start} disabled={busy || selected.size === 0}>
          Start {selected.size > 0 ? `${selected.size} selected` : "selected"}
        </button>
      </div>
      {error && <p className="status">{error}</p>}
      {reply && <StartReplyPanel reply={reply} onClose={() => setReply(null)} />}
      {projects?.length === 0 && <p className="status">No projects yet.</p>}
      {issues?.length === 0 && <p className="status">No issues yet: label a GitHub issue to sync it in.</p>}
      {rows.length > 0 && (
        <table className="issues">
          <thead><tr><th /><th>Issue</th><th>Status</th><th>Waits for</th><th>Cost</th><th>Run</th></tr></thead>
          <tbody>
            {rows.map(({ issue: i, depth }) => (
              <tr key={i.key}>
                <td><input type="checkbox" aria-label={`select ${i.key}`} checked={selected.has(i.key)} onChange={() => toggle(i.key)}
                  disabled={isFinished(i.status)} /></td>
                <td style={{ paddingLeft: 8 + depth * 18 }}>
                  <a href={`#/issues/${i.key}`}>{i.key}</a> {i.title} <span className="type">· {i.type}</span>
                  {i.start_requested_at && <span className="type" title="someone asked to start it"> · requested</span>}
                </td>
                <td>
                  <StatusChip status={i.status} />
                  {issues && <Progress statuses={leafStatuses(issues, i.key)} />}
                </td>
                <td>{i.depends_on.map((d, n) => <span key={d}>{n > 0 && ", "}<a href={`#/issues/${d}`}>{d}</a></span>)}</td>
                <td>{i.cost_usd > 0 ? usd(i.cost_usd) : ""}</td>
                <td>{i.latest_run && <a href={`#/runs/${i.latest_run.id}`}>{i.latest_run.status}</a>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {issues && <DependencyGraph issues={issues.filter((i) => showFinished || !isFinished(i.status) || issues.some((o) => o.depends_on.includes(i.key) && !isFinished(o.status)))} />}
    </>
  );
}

// Progress shows how far a parent is, over the leaves under it (D70).
function Progress({ statuses }: { statuses: string[] }) {
  if (statuses.length === 0) return null;
  return <span className="type"> {progressText(progress(statuses))}</span>;
}

export function StatusChip({ status }: { status: string }) {
  return <span className={`chip st-${status}`}>{status.replace("_", " ")}</span>;
}

export function StartReplyPanel({ reply, onClose }: { reply: StartReply; onClose: () => void }) {
  return (
    <section className="approval">
      <h2>Start requested: {reply.requested.join(", ")}</h2>
      {reply.starting.length > 0 && <p>Starting on the next pass: {reply.starting.join(", ")}</p>}
      {reply.waiting.length > 0 && (
        <ul>{reply.waiting.map((w) => <li key={w.key}><a href={`#/issues/${w.key}`}>{w.key}</a>: {w.reason}</li>)}</ul>
      )}
      {reply.overlaps.length > 0 && (
        <>
          <p className="question">Likely overlap with work in flight (a warning; nothing is held back):</p>
          <ul>{reply.overlaps.map((o) => <li key={o.key + o.with}>{o.key} and {o.with} both touch {o.paths.join(", ")}</li>)}</ul>
        </>
      )}
      <div className="actions"><button className="reject" onClick={onClose}>Dismiss</button></div>
    </section>
  );
}

const NODE_W = 190, NODE_H = 34, GAP_X = 56, GAP_Y = 14;

// DependencyGraph draws the issues that wait for others, in layers: an arrow
// leads from an issue to what it waits for, which sits to its left.
function DependencyGraph({ issues }: { issues: IssueSummary[] }) {
  const { nodes, edges } = dependencyGraph(issues);
  if (nodes.length === 0) return null;
  const at = new Map(nodes.map((n) => [n.key, { x: n.layer * (NODE_W + GAP_X), y: n.row * (NODE_H + GAP_Y) }]));
  const width = Math.max(...nodes.map((n) => n.layer)) * (NODE_W + GAP_X) + NODE_W + 2;
  const height = Math.max(...nodes.map((n) => n.row)) * (NODE_H + GAP_Y) + NODE_H + 2;
  return (
    <section>
      <h2 className="type">dependencies</h2>
      <div className="graph">
        <svg width={width} height={height} role="img" aria-label="issue dependency graph">
          <defs>
            <marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M0,0 L10,5 L0,10 z" className="edge-head" />
            </marker>
          </defs>
          {edges.map((e) => {
            const a = at.get(e.from)!, b = at.get(e.to)!;
            const x1 = a.x, y1 = a.y + NODE_H / 2, x2 = b.x + NODE_W, y2 = b.y + NODE_H / 2;
            const mid = (x1 + x2) / 2;
            return <path key={e.from + e.to} className="edge" d={`M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`} markerEnd="url(#arrow)" />;
          })}
          {nodes.map((n) => {
            const p = at.get(n.key)!;
            return (
              <a key={n.key} href={`#/issues/${n.key}`}>
                <title>{`${n.key} ${n.title} (${n.status})`}</title>
                <rect x={p.x + 1} y={p.y + 1} width={NODE_W - 2} height={NODE_H - 2} rx="6" className={`node st-${n.status}`} />
                <text x={p.x + 10} y={p.y + NODE_H / 2 + 4} className="node-label">
                  {n.key} · {n.status.replace("_", " ")}
                </text>
              </a>
            );
          })}
        </svg>
      </div>
    </section>
  );
}
