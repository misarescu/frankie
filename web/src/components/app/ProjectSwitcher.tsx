import { useNavigate, useRouterState } from "@tanstack/react-router";
import { CheckIcon, ChevronsUpDownIcon } from "lucide-react";
import { switchProjectHref } from "@/components/app/nav";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { projectTitle } from "@/crumbs";
import type { Project } from "@/issues";
import { saveLastProject } from "@/prefs";

// ProjectSwitcher picks the project the board, runs and releases show; the
// choice is remembered (frankie.project) for the next visit.
export function ProjectSwitcher({ project, projects, failed, onNavigate }: { project: Project | undefined; projects: Project[]; failed: boolean; onNavigate?: () => void }) {
  const navigate = useNavigate();
  const href = useRouterState({ select: (s) => s.location.href });
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className="flex w-full cursor-pointer items-center gap-2 rounded-md border border-line bg-card px-2.5 py-2 text-left hover:bg-hover"
        aria-label={project ? `Project: ${projectTitle(project)}. Switch project` : "Pick a project"}
      >
        <span className="mono grid h-6 min-w-7 place-items-center rounded bg-muted px-1 text-xs2 font-medium">{project?.key_prefix ?? "?"}</span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[13px] font-medium">{project ? projectTitle(project) : "No project"}</span>
          <span className="block truncate text-xs2 text-muted-fg">{project?.name ?? (failed ? "projects did not load" : projects.length ? "pick one" : "none configured")}</span>
        </span>
        <ChevronsUpDownIcon className="size-3.5 shrink-0 text-muted-fg" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-(--radix-dropdown-menu-trigger-width)">
        <DropdownMenuLabel>Projects</DropdownMenuLabel>
        {projects.map((p) => (
          <DropdownMenuItem
            key={p.id}
            onSelect={() => {
              saveLastProject(p.key_prefix);
              onNavigate?.();
              void navigate({ href: switchProjectHref(href, p.key_prefix) });
            }}
          >
            <span className="mono w-9 shrink-0 text-xs text-muted-fg">{p.key_prefix}</span>
            <span className="min-w-0 flex-1 truncate">{projectTitle(p)}</span>
            {p.key_prefix === project?.key_prefix && <CheckIcon className="size-3.5 text-primary" />}
          </DropdownMenuItem>
        ))}
        {projects.length === 0 && <div className="px-2 py-1.5 text-xs text-muted-fg">{failed ? "The projects did not load." : "No projects yet."}</div>}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
