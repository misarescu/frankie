import { useQuery } from "@tanstack/react-query";
import { getRouteApi, Outlet, useRouterState } from "@tanstack/react-router";
import { FolderXIcon } from "lucide-react";
import { createContext, useContext, useEffect } from "react";
import { projectsQuery } from "@/api/queries";
import { EmptyState, ErrorState, LoadingState } from "@/components/app/states";
import { findProject, keyPrefix, type Project } from "@/issues";
import { loadLastProject, saveLastProject, startProject } from "@/prefs";

const ProjectContext = createContext<Project | null>(null);

const projectRoute = getRouteApi("/projects/$project");

// ProjectLayout resolves /projects/$project to its project for the board,
// runs and releases pages, remembers it as the last one opened, and says so
// when no project has that key prefix.
export function ProjectLayout() {
  const { project: prefix } = projectRoute.useParams();
  const projects = useQuery(projectsQuery());
  const project = projects.data && findProject(projects.data, prefix);
  useEffect(() => { if (project) saveLastProject(project.key_prefix); }, [project]);
  if (projects.isPending) return <LoadingState />;
  if (projects.isError) return <ErrorState error={projects.error} what="the projects" onRetry={() => void projects.refetch()} />;
  if (!project) {
    return (
      <EmptyState icon={<FolderXIcon className="size-6" />} title={`No project "${prefix}"`}>
        No project has the key prefix {prefix}. Pick one in the sidebar.
      </EmptyState>
    );
  }
  return (
    <ProjectContext.Provider value={project}>
      <Outlet />
    </ProjectContext.Provider>
  );
}

// useProject is the project of a /projects/$project page.
export function useProject(): Project {
  const p = useContext(ProjectContext);
  if (!p) throw new Error("useProject outside a /projects/$project page");
  return p;
}

// pathPrefix is the project a path names: /projects/gc/... or /issues/gc-83.
function pathPrefix(path: string): string | undefined {
  const project = /^\/projects\/([^/]+)/.exec(path)?.[1];
  if (project) return decodeURIComponent(project);
  const issue = /^\/issues\/([^/]+)/.exec(path)?.[1];
  return issue ? keyPrefix(decodeURIComponent(issue)) : undefined;
}

// useCurrentProject is the project the shell shows (sidebar, palette): the
// page's, else the one of the issue in the path, else the last one opened.
// Pages under no project (a run, a release, approvals) keep the last one.
export function useCurrentProject(): { project: Project | undefined; projects: Project[] } {
  const projects = useQuery(projectsQuery()).data ?? [];
  const path = useRouterState({ select: (s) => s.location.pathname });
  const fromPath = pathPrefix(path);
  const prefix = (fromPath && findProject(projects, fromPath)?.key_prefix) ?? startProject(projects.map((p) => p.key_prefix), loadLastProject());
  return { project: prefix ? findProject(projects, prefix) : undefined, projects };
}

// projectTitle is how the shell names a project: its repository's name,
// without the owner.
export const projectTitle = (p: Project): string => p.name.split("/").at(-1) || p.name;
