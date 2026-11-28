import { getRouteApi } from "@tanstack/react-router";
import { PageHeader } from "@/components/app/PageHeader";

const route = getRouteApi("/projects/$project/runs");

// RunsPage: a stub until its page is ported (M9).
export function RunsPage() {
  const params = route.useParams();
  const search = route.useSearch();
  return (
    <div className="space-y-5">
      <PageHeader title="Runs" sub={<span className="mono">{JSON.stringify({ params, search })}</span>} />
    </div>
  );
}
