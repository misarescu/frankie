import { getRouteApi } from "@tanstack/react-router";
import { PageHeader } from "@/components/app/PageHeader";

const route = getRouteApi("/issues/$key");

// IssuePage: a stub until its page is ported (M9).
export function IssuePage() {
  const params = route.useParams();
  const search = route.useSearch();
  return (
    <div className="space-y-5">
      <PageHeader title="Issue" sub={<span className="mono">{JSON.stringify({ params, search })}</span>} />
    </div>
  );
}
