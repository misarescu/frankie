import { getRouteApi } from "@tanstack/react-router";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { PageHeader } from "@/components/app/PageHeader";
import { useCurrentProject } from "@/components/app/project";
import { projectCrumb } from "@/crumbs";
import { paths } from "@/paths";

const route = getRouteApi("/releases/$id");

// ReleasePage: a stub until its page is ported (M9).
export function ReleasePage() {
  const { id } = route.useParams();
  const { project } = useCurrentProject();
  useCrumbs([...(project ? [projectCrumb(project), { label: "Releases", href: paths.releases(project.key_prefix) }] : []), { label: "Release" }]);
  return (
    <div className="space-y-5">
      <PageHeader title="Release" sub={<span className="mono">{JSON.stringify({ id })}</span>} />
    </div>
  );
}
