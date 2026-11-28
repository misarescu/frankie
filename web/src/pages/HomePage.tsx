import { useQuery } from "@tanstack/react-query";
import { FolderGit2Icon } from "lucide-react";
import { projectsQuery } from "@/api/queries";
import { EmptyState, ErrorState, LoadingState } from "@/components/app/states";

// HomePage is / when it cannot open a project's board: the projects did not
// load, or there are none yet. Otherwise / redirects (router.tsx).
export function HomePage() {
  const projects = useQuery(projectsQuery());
  if (projects.isPending) return <LoadingState />;
  if (projects.isError) return <ErrorState level={1} error={projects.error} what="the projects" onRetry={() => void projects.refetch()} />;
  return (
    <EmptyState level={1} icon={<FolderGit2Icon className="size-6" />} title="No projects yet">
      Frankie works on the repositories its deployment lists. Add one there and its issues show up here.
    </EmptyState>
  );
}
