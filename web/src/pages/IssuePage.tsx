import { getRouteApi } from "@tanstack/react-router";
import { useIssueCrumbs } from "@/components/app/Breadcrumbs";
import { PageHeader } from "@/components/app/PageHeader";

const route = getRouteApi("/issues/$key");

// IssuePage: a stub until its page is ported (M9).
export function IssuePage() {
  const { key } = route.useParams();
  const search = route.useSearch();
  useIssueCrumbs(key);
  return (
    <div className="space-y-5">
      <PageHeader title={key} sub={<span className="mono">{JSON.stringify({ key, search })}</span>} />
    </div>
  );
}
