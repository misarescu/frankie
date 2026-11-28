// The route tree (D141): path URLs with typed search params. Every tab,
// view, filter and selected step is a search param, so each change is a
// history entry that back and forward walk and a reload restores.

import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, createRoute, createRouter, redirect } from "@tanstack/react-router";
import { projectsQuery } from "@/api/queries";
import { ProjectLayout } from "@/components/app/project";
import { Shell } from "@/components/app/Shell";
import { ApprovalsPage } from "@/pages/ApprovalsPage";
import { BoardPage } from "@/pages/BoardPage";
import { HomePage } from "@/pages/HomePage";
import { IssuePage } from "@/pages/IssuePage";
import { NotFoundPage } from "@/pages/NotFoundPage";
import { ReleasePage } from "@/pages/ReleasePage";
import { ReleasesPage } from "@/pages/ReleasesPage";
import { RunPage } from "@/pages/RunPage";
import { RunsPage } from "@/pages/RunsPage";
import { loadLastProject, startProject } from "@/prefs";
import { legacyPath } from "@/routes";
import { approvalsSearch, boardSearch, issueSearch, runSearch, runsSearch } from "@/search";

export type RouterContext = { queryClient: QueryClient };

// homeProject is the project / opens: the last one opened, else the first.
async function homeProject(qc: QueryClient): Promise<string | undefined> {
  try {
    const projects = await qc.ensureQueryData(projectsQuery());
    return startProject(projects.map((p) => p.key_prefix), loadLastProject());
  } catch {
    return undefined; // the home page shows why
  }
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  // A pre-M9 hash URL (#/issues/gc-12) redirects to its path.
  beforeLoad: async ({ location, context }) => {
    if (!/^#?\//.test(location.hash)) return;
    const project = await homeProject(context.queryClient);
    const to = legacyPath(location.hash, project ?? "");
    if (to && (project || !to.startsWith("/projects/"))) throw redirect({ href: to, replace: true });
    if (to) throw redirect({ to: "/", replace: true });
  },
  component: Shell,
  notFoundComponent: NotFoundPage,
});

const homeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: async ({ context }) => {
    const project = await homeProject(context.queryClient);
    if (project) throw redirect({ to: "/projects/$project/board", params: { project }, replace: true });
  },
  component: HomePage,
});

const projectRoute = createRoute({ getParentRoute: () => rootRoute, path: "/projects/$project", component: ProjectLayout });

const boardRoute = createRoute({ getParentRoute: () => projectRoute, path: "/board", validateSearch: boardSearch, component: BoardPage });
const runsRoute = createRoute({ getParentRoute: () => projectRoute, path: "/runs", validateSearch: runsSearch, component: RunsPage });
const releasesRoute = createRoute({ getParentRoute: () => projectRoute, path: "/releases", component: ReleasesPage });

const issueRoute = createRoute({ getParentRoute: () => rootRoute, path: "/issues/$key", validateSearch: issueSearch, component: IssuePage });
const runRoute = createRoute({ getParentRoute: () => rootRoute, path: "/runs/$id", validateSearch: runSearch, component: RunPage });
const releaseRoute = createRoute({ getParentRoute: () => rootRoute, path: "/releases/$id", component: ReleasePage });
const approvalsRoute = createRoute({ getParentRoute: () => rootRoute, path: "/approvals", validateSearch: approvalsSearch, component: ApprovalsPage });

const routeTree = rootRoute.addChildren([
  homeRoute,
  projectRoute.addChildren([boardRoute, runsRoute, releasesRoute]),
  issueRoute,
  runRoute,
  releaseRoute,
  approvalsRoute,
]);

export function createAppRouter(queryClient: QueryClient) {
  return createRouter({
    routeTree,
    context: { queryClient },
    scrollRestoration: true,
    defaultPreload: "intent",
    defaultPreloadStaleTime: 0,
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
