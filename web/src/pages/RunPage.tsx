import { getRouteApi } from "@tanstack/react-router";
import { useCrumbs } from "@/components/app/Breadcrumbs";
import { PageHeader } from "@/components/app/PageHeader";
import { shortID } from "@/issues";

const route = getRouteApi("/runs/$id");

// RunPage: a stub until its page is ported (M9). The ported page sets its
// crumbs with useIssueCrumbs(run's issue, { tail: [run, step] }).
export function RunPage() {
  const { id } = route.useParams();
  const search = route.useSearch();
  useCrumbs([{ label: `Run ${shortID(id)}` }]);
  return (
    <div className="space-y-5">
      <PageHeader title="Run" sub={<span className="mono">{JSON.stringify({ id, search })}</span>} />
    </div>
  );
}
