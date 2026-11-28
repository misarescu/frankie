import { getRouteApi } from "@tanstack/react-router";
import { PageHeader } from "@/components/app/PageHeader";

const route = getRouteApi("/releases/$id");

// ReleasePage: a stub until its page is ported (M9).
export function ReleasePage() {
  const params = route.useParams();
  return (
    <div className="space-y-5">
      <PageHeader title="Release" sub={<span className="mono">{JSON.stringify({ params })}</span>} />
    </div>
  );
}
