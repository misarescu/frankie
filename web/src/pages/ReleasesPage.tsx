import { getRouteApi } from "@tanstack/react-router";
import { PageHeader } from "@/components/app/PageHeader";

const route = getRouteApi("/projects/$project/releases");

// ReleasesPage: a stub until its page is ported (M9).
export function ReleasesPage() {
  const params = route.useParams();
  return (
    <div className="space-y-5">
      <PageHeader title="Releases" sub={<span className="mono">{JSON.stringify({ params })}</span>} />
    </div>
  );
}
