import { useCallback, useEffect, useState } from "react";
import type { Project } from "./issues";
import { remember, remembered, StatusChip } from "./IssuesPage";
import { isLive, releaseName, shortSHA, type ReleaseSummary } from "./releases";

const getJSON = <T,>(url: string): Promise<T> =>
  fetch(url).then((r) => (r.ok ? r.json() : Promise.reject(new Error(`status ${r.status}`))));

// The releases page (D130): a project's releases, newest first, with their
// version, status, issues and how far they got through the stages.
export function ReleasesPage() {
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [project, setProject] = useState<string | null>(null);
  const [releases, setReleases] = useState<ReleaseSummary[] | null>(null);
  const [error, setError] = useState<string | null>(null);

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
    getJSON<ReleaseSummary[]>(`/api/releases?project=${encodeURIComponent(project)}`)
      .then((rs) => { setReleases(rs); setError(null); })
      .catch((e: Error) => setError(e.message));
  }, [project]);
  const live = releases?.some((r) => isLive(r.status)) ?? false;
  useEffect(() => {
    load();
    const t = setInterval(load, live ? 5000 : 15000);
    return () => clearInterval(t);
  }, [load, live]);

  return (
    <>
      <h1>
        Releases <span className="type">· <a href="#/">Runs</a> · <a href="#/issues">Issues</a> · <a href="#/approvals">Approvals</a></span>
      </h1>
      <div className="actions">
        {projects && projects.length > 1 && (
          <select value={project ?? ""} onChange={(e) => { setReleases(null); setProject(e.target.value); remember(e.target.value); }}>
            {projects.map((p) => <option key={p.id} value={p.name}>{p.name}</option>)}
          </select>
        )}
        {projects?.length === 1 && <span className="status">{project}</span>}
      </div>
      {error && <p className="status">{error}</p>}
      {projects?.length === 0 && <p className="status">No projects yet.</p>}
      {releases?.length === 0 && <p className="status">No releases yet: a merge from the merge queue asks for one when the project declares stages.</p>}
      {releases && releases.length > 0 && (
        <table className="releases">
          <thead><tr><th>Release</th><th>Status</th><th>Stages</th><th>Issues</th><th>Created</th></tr></thead>
          <tbody>
            {releases.map((r) => (
              <tr key={r.id}>
                <td>
                  <a href={`#/releases/${r.id}`}>{releaseName(r)}</a>
                  {r.version && <span className="type" title={r.sha}> · {shortSHA(r.sha)}</span>}
                  {r.bump && <span className="type"> · {r.bump}</span>}
                </td>
                <td><StatusChip status={r.status} /></td>
                <td>
                  {r.stages.map((s) => <span key={s.stage} className={`chip st-${s.status}`} title={s.status}>{s.stage}</span>)}
                </td>
                <td>{r.issues.map((k, n) => <span key={k}>{n > 0 && ", "}<a href={`#/issues/${k}`}>{k}</a></span>)}</td>
                <td>{new Date(r.created_at).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
