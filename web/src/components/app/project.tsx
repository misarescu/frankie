import { useQuery } from "@tanstack/react-query";
import { getRouteApi, Outlet, useRouterState } from "@tanstack/react-router";
import { FolderXIcon } from "lucide-react";
import { createContext, useContext, useEffect } from "react";
import { getJSON, keys, projectsQuery, releaseQuery, runsQuery } from "@/api/queries";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { entityProject, pageEntity } from "@/components/app/nav";
import { EmptyState, ErrorState, LoadingState } from "@/components/app/states";
import type { RunSummary } from "@/events";
import { findProject, type Project } from "@/issues";
import { loadLastProject, saveLastProject, startProject } from "@/prefs";

const ProjectContext = createContext<Project | null>(null);

const projectRoute = getRouteApi("/projects/$project");

// ProjectLayout resolves /projects/$project to its project for the board,
// runs and releases pages, and says so when no project has that key prefix.
export function ProjectLayout() {
  const { project: prefix } = projectRoute.useParams();
  const projects = useQuery(projectsQuery());
  const project = projects.data && findProject(projects.data, prefix);
  if (projects.isPending) return <LoadingState />;
  if (projects.isError) return <ErrorState level={1} error={projects.error} what="the projects" onRetry={() => void projects.refetch()} />;
  if (!project) return <UnknownProject prefix={prefix} />;
  return (
    <ProjectContext.Provider value={project}>
      <Outlet />
    </ProjectContext.Provider>
  );
}

function UnknownProject({ prefix }: { prefix: string }) {
  useCrumbs([{ label: `No project "${prefix}"` }]);
  return (
    <EmptyState level={1} icon={<FolderXIcon className="size-6" />} title={`No project "${prefix}"`}>
      No project has the key prefix {prefix}. Pick one in the sidebar.
    </EmptyState>
  );
}

// useProject is the project of a /projects/$project page.
export function useProject(): Project {
  const p = useContext(ProjectContext);
  if (!p) throw new Error("useProject outside a /projects/$project page");
  return p;
}

// useRunSummary finds a run among the recent runs (GET /api/runs), and
// among more of them when it is older than those.
export function useRunSummary(id: string | undefined): RunSummary | undefined {
  const recent = useQuery({ ...runsQuery(), enabled: !!id });
  const found = id ? recent.data?.find((r) => r.ID === id) : undefined;
  const older = useQuery({
    queryKey: [...keys.runs, "older"],
    queryFn: () => getJSON<RunSummary[]>("/api/runs?limit=200"),
    enabled: !!id && recent.isSuccess && !found,
    staleTime: 60_000,
  });
  return found ?? (id ? older.data?.find((r) => r.ID === id) : undefined);
}

// useCurrentProject is the project the shell shows (sidebar, palette): the
// one of what the page shows (a project's page, an issue, a run, a release),
// which becomes the remembered one, else the last one opened. Approvals
// belong to no project and keep the last one.
export function useCurrentProject(): { project: Project | undefined; projects: Project[]; failed: boolean; loading: boolean } {
  const q = useQuery(projectsQuery());
  const projects = q.data ?? [];
  const path = useRouterState({ select: (s) => s.location.pathname });
  const entity = pageEntity(path);
  const run = useRunSummary(entity?.kind === "run" ? entity.id : undefined);
  const releaseID = entity?.kind === "release" ? entity.id : run?.ReleaseID || undefined;
  const release = useQuery({ ...releaseQuery(releaseID ?? ""), enabled: !!releaseID }).data;
  const shown = entityProject(entity, projects, { run, release });
  useEffect(() => { if (shown) saveLastProject(shown); }, [shown]);
  const prefix = shown ?? startProject(projects.map((p) => p.key_prefix), loadLastProject());
  return { project: prefix ? findProject(projects, prefix) : undefined, projects, failed: q.isError, loading: q.isPending };
}
