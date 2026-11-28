import { getRouteApi } from "@tanstack/react-router";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { PageHeader } from "@/components/app/PageHeader";
import { useProject } from "@/components/app/project";
import { projectCrumb } from "@/crumbs";

const route = getRouteApi("/projects/$project/releases");

// ReleasesPage: a stub until its page is ported (M9).
export function ReleasesPage() {
  const project = useProject();
  const params = route.useParams();
  useCrumbs([projectCrumb(project), { label: "Releases" }]);
  return (
    <div className="space-y-5">
      <PageHeader title="Releases" sub={<span className="mono">{JSON.stringify({ project: project.name, params })}</span>} />
    </div>
  );
}
