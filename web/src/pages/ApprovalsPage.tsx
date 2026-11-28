import { getRouteApi } from "@tanstack/react-router";
import { PageHeader } from "@/components/app/PageHeader";

const route = getRouteApi("/approvals");

// ApprovalsPage: a stub until its page is ported (M9).
export function ApprovalsPage() {
  const search = route.useSearch();
  return (
    <div className="space-y-5">
      <PageHeader title="Approvals" sub={<span className="mono">{JSON.stringify({ search })}</span>} />
    </div>
  );
}
