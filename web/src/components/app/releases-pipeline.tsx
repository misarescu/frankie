import { ChevronRightIcon } from "lucide-react";
import { Fragment } from "react";
import { StatusBadge } from "@/components/app/StatusBadge";
import { cn } from "@/lib/utils";
import type { StageStatus } from "@/releases";

// Pipeline is a release's stages in order, each a chip with its status (the
// mockup's): small in the releases list, boxed on the release page.
export function Pipeline({ stages, big = false, className }: { stages: StageStatus[]; big?: boolean; className?: string }) {
  if (stages.length === 0) return <span className={cn("text-xs text-muted-fg", className)}>no stage started</span>;
  return (
    <div className={cn("flex min-w-0 flex-wrap items-center gap-1.5", className)} aria-label="Stages">
      {stages.map((s, n) => (
        <Fragment key={s.stage}>
          {n > 0 && <ChevronRightIcon className="size-3.5 shrink-0 text-muted-fg" aria-hidden="true" />}
          <span className={cn("inline-flex min-w-0 items-center gap-1.5", big && "rounded-md border border-line bg-card px-2.5 py-1.5")}>
            <span className="mono text-xs [overflow-wrap:anywhere]">{s.stage}</span>
            <StatusBadge status={s.status} />
          </span>
        </Fragment>
      ))}
    </div>
  );
}
