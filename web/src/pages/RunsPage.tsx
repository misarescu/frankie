import { getRouteApi } from "@tanstack/react-router";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { PageHeader } from "@/components/app/PageHeader";
import { useProject } from "@/components/app/project";
import { projectCrumb } from "@/crumbs";

const route = getRouteApi("/projects/$project/runs");

// RunsPage: a stub until its page is ported (M9).
export function RunsPage() {
  const project = useProject();
  const params = route.useParams();
  const search = route.useSearch();
  useCrumbs([projectCrumb(project), { label: "Runs" }]);
  return (
    <div className="space-y-5">
      <PageHeader title="Runs" sub={<span className="mono">{JSON.stringify({ project: project.name, params, search })}</span>} />
    </div>
  );
}
