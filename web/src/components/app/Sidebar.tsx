import { useQuery } from "@tanstack/react-query";
import { useRouterState } from "@tanstack/react-router";
import { approvalsQuery, meQuery, questionsQuery, specsQuery } from "@/api/queries";
import { isAdmin } from "@/auth";
import { AccountMenu } from "@/components/app/AccountMenu";
import { AppLink } from "@/components/app/AppLink";
import { activeNav, navItems, settingsNavItems } from "@/components/app/nav";
import { useCurrentProject } from "@/components/app/project";
import { ProjectSwitcher } from "@/components/app/ProjectSwitcher";
import { cn } from "@/lib/utils";

// useWaiting counts what waits for a person: human gates, questions and specs.
export function useWaiting(): number {
  const gates = useQuery(approvalsQuery()).data?.length ?? 0;
  const questions = useQuery(questionsQuery()).data?.length ?? 0;
  const specs = useQuery(specsQuery()).data?.length ?? 0;
  return gates + questions + specs;
}

// Sidebar: the project switcher, the pages, settings links and, at its foot,
// the signed-in user menu. At phone width it is the drawer's content.
export function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const { project, projects, failed, loading } = useCurrentProject();
  const me = useQuery(meQuery());
  const admin = isAdmin(me.data?.role);
  const path = useRouterState({ select: (s) => s.location.pathname });
  const active = activeNav(path);
  const waiting = useWaiting();
  const settings = settingsNavItems.filter((item) => !item.adminOnly || admin);

  return (
    <div className="flex h-full flex-col gap-4 bg-sidebar px-3 py-4" style={{ paddingLeft: "max(12px, env(safe-area-inset-left, 0px))" }}>
      <AppLink href="/" onClick={onNavigate} className="flex w-fit items-center gap-2 px-2">
        <span className="grid size-7 place-items-center rounded-md bg-primary text-[13px] font-bold text-primary-fg">F</span>
        <span className="text-[15px] font-semibold tracking-tight">Frankie</span>
      </AppLink>
      <ProjectSwitcher project={project} projects={projects} failed={failed} loading={loading} onNavigate={onNavigate} />
      <nav aria-label="Main">
        <ul className="space-y-0.5">
          {navItems.map((item) => (
            <li key={item.id}>
              <AppLink
                href={item.href(project?.key_prefix)}
                onClick={onNavigate}
                aria-current={active === item.id ? "page" : undefined}
                className={cn("flex w-full items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13px] font-medium",
                  active === item.id ? "bg-primary-soft text-primary" : "text-muted-fg hover:bg-hover hover:text-fg")}
              >
                <item.icon className="size-4 shrink-0" />
                {item.label}
                {item.id === "approvals" && waiting > 0 && (
                  <span className="tnum ml-auto rounded-full bg-warn px-1.5 text-xs2 font-semibold text-bg" aria-label={`${waiting} waiting`}>{waiting}</span>
                )}
              </AppLink>
            </li>
          ))}
        </ul>
      </nav>
      <nav aria-label="Settings" className="border-t border-line pt-3">
        <div className="px-2.5 pb-1 text-xs2 font-medium tracking-wide text-muted-fg uppercase">Settings</div>
        <ul className="space-y-0.5">
          {settings.map((item) => (
            <li key={item.id}>
              <AppLink
                href={item.href}
                onClick={onNavigate}
                aria-current={active === item.id ? "page" : undefined}
                className={cn("flex w-full items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13px] font-medium",
                  active === item.id ? "bg-primary-soft text-primary" : "text-muted-fg hover:bg-hover hover:text-fg")}
              >
                <item.icon className="size-4 shrink-0" />
                {item.label}
              </AppLink>
            </li>
          ))}
        </ul>
      </nav>
      <div className="mt-auto">
        <AccountMenu onNavigate={onNavigate} />
      </div>
    </div>
  );
}
