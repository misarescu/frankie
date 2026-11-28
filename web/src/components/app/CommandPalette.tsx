import { useQuery } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { ActivityIcon } from "lucide-react";
import { issuesQuery, runsQuery } from "@/api/queries";
import { navItems } from "@/components/app/nav";
import { useCurrentProject } from "@/components/app/project";
import { StatusBadge } from "@/components/app/StatusBadge";
import { TypeIcon } from "@/components/app/TypeIcon";
import { Command, CommandDialog, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command";
import { Kbd } from "@/components/ui/kbd";
import { paths } from "@/paths";
import { releaseName } from "@/releases";

const RECENT_RUNS = 8;

// CommandPalette (Cmd-K, Ctrl-K) jumps to a page, an issue of the current
// project by key or title, or a recent run. The first match stays selected,
// so Enter opens it.
export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const router = useRouter();
  const { project } = useCurrentProject();
  const issues = useQuery({ ...issuesQuery(project?.name ?? ""), enabled: open && !!project }).data ?? [];
  const runs = (useQuery({ ...runsQuery(), enabled: open }).data ?? []).slice(0, RECENT_RUNS);
  const go = (href: string) => {
    onOpenChange(false);
    void router.navigate({ href });
  };
  return (
    <CommandDialog open={open} onOpenChange={onOpenChange} title="Search" description="Jump to an issue, a run or a page">
      <Command loop>
        <CommandInput placeholder="Jump to an issue key, a run or a page" right={<Kbd>esc</Kbd>} />
        <CommandList>
          <CommandEmpty>No issue, run or page matches.</CommandEmpty>
          <CommandGroup heading="Pages">
            {navItems.map((item) => (
              <CommandItem key={item.id} value={`page ${item.label}`} onSelect={() => go(item.href(project?.key_prefix))}>
                <item.icon className="size-4 text-muted-fg" />
                {item.label}
              </CommandItem>
            ))}
          </CommandGroup>
          {issues.length > 0 && (
            <CommandGroup heading="Issues">
              {issues.map((i) => (
                <CommandItem key={i.key} value={`${i.key} ${i.title}`} onSelect={() => go(paths.issue(i.key))}>
                  <TypeIcon type={i.type} />
                  <span className="mono shrink-0 text-xs text-muted-fg">{i.key}</span>
                  <span className="min-w-0 flex-1 truncate">{i.title}</span>
                  <StatusBadge status={i.status} />
                </CommandItem>
              ))}
            </CommandGroup>
          )}
          {runs.length > 0 && (
            <CommandGroup heading="Recent runs">
              {runs.map((r) => {
                const what = r.IssueKey ? `${r.IssueKey} ${r.IssueTitle}` : `release ${r.ReleaseVersion || releaseName({ sha: r.ReleaseSHA ?? "" }) || r.ReleaseID || ""}`;
                return (
                  <CommandItem key={r.ID} value={`run ${r.ID} ${what}`} onSelect={() => go(paths.run(r.ID))}>
                    <ActivityIcon className="size-4 text-muted-fg" />
                    <span className="min-w-0 flex-1 truncate">Run of {what}</span>
                    <StatusBadge status={r.Status} />
                  </CommandItem>
                );
              })}
            </CommandGroup>
          )}
        </CommandList>
      </Command>
    </CommandDialog>
  );
}
