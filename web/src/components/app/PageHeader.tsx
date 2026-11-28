import type { ReactNode } from "react";

// PageHeader is a page's title, with what it shows beneath and its actions on the right.
export function PageHeader({ title, sub, icon, actions }: { title: ReactNode; sub?: ReactNode; icon?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        <h1 className="flex min-w-0 items-center gap-2 text-xl font-semibold tracking-tight [overflow-wrap:anywhere]">{icon}{title}</h1>
        {sub && <div className="mt-0.5 text-[13px] text-muted-fg">{sub}</div>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}
