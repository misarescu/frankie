import { useQuery } from "@tanstack/react-query";
import { ChevronRightIcon } from "lucide-react";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { issuesQuery, projectsQuery } from "@/api/queries";
import { AppLink } from "@/components/app/AppLink";
import { issueChain, pageTitle, type Crumb } from "@/crumbs";
import { findProject, keyPrefix } from "@/issues";
import { cn } from "@/lib/utils";

const CrumbsContext = createContext<{ crumbs: Crumb[]; set: (c: Crumb[]) => void }>({ crumbs: [], set: () => {} });

export function CrumbsProvider({ children }: { children: ReactNode }) {
  const [crumbs, set] = useState<Crumb[]>([]);
  return <CrumbsContext.Provider value={{ crumbs, set }}>{children}</CrumbsContext.Provider>;
}

// useCrumbs sets the header's breadcrumbs while the calling page shows.
export function useCrumbs(crumbs: Crumb[]): void {
  const { set } = useContext(CrumbsContext);
  const key = JSON.stringify(crumbs);
  useEffect(() => {
    set(JSON.parse(key) as Crumb[]);
    return () => set([]);
  }, [key, set]);
}

// useIssueCrumbs sets the crumbs down to an issue (project > epic > story >
// task), then tail: the run page adds its run and step. title is the issue's
// own, for before the project's issue list has loaded.
export function useIssueCrumbs(key: string, opts: { title?: string; tail?: Crumb[] } = {}): void {
  const project = findProject(useQuery(projectsQuery()).data ?? [], keyPrefix(key));
  const issues = useQuery({ ...issuesQuery(project?.name ?? ""), enabled: !!project }).data ?? [];
  const tail = opts.tail ?? [];
  useCrumbs([...issueChain(project, issues, key, { titles: tail.length === 0, title: opts.title, linkLast: tail.length > 0 }), ...tail]);
}

// Breadcrumbs shows the page's crumbs, at phone width only the last two,
// and names the page in the browser's tab from them.
export function Breadcrumbs() {
  const { crumbs } = useContext(CrumbsContext);
  useEffect(() => { document.title = pageTitle(crumbs); }, [crumbs]);
  return (
    <nav aria-label="Breadcrumb" className="min-w-0 flex-1">
      <ol className="flex min-w-0 items-center gap-1 overflow-hidden text-[13px]">
        {crumbs.map((c, n) => {
          const last = n === crumbs.length - 1;
          return (
            <li key={n} className={cn("flex min-w-0 items-center gap-1", n < crumbs.length - 2 && "hidden sm:flex", !last && "shrink-[2]")}>
              {n > 0 && <ChevronRightIcon className={cn("size-3.5 shrink-0 text-muted-fg", n === crumbs.length - 2 && "hidden sm:block")} aria-hidden="true" />}
              {c.href && !last
                ? <AppLink href={c.href} className="truncate text-muted-fg hover:text-fg" title={c.label}>{c.label}</AppLink>
                : <span className="truncate font-medium" aria-current={last ? "page" : undefined} title={c.label}>{c.label}</span>}
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
