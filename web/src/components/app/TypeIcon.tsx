import { BookOpenIcon, BugIcon, LayersIcon, SquareCheckIcon } from "lucide-react";
import { cn } from "@/lib/utils";

const types = {
  epic: { icon: LayersIcon, color: "text-primary" },
  story: { icon: BookOpenIcon, color: "text-ok" },
  task: { icon: SquareCheckIcon, color: "text-muted-fg" },
  bug: { icon: BugIcon, color: "text-bad" },
};

// TypeIcon marks an issue's type: epic, story, task or bug.
export function TypeIcon({ type, className }: { type: string; className?: string }) {
  const t = types[type as keyof typeof types] ?? types.task;
  return (
    <span className={cn("inline-flex shrink-0", t.color)} title={type}>
      <t.icon className={cn("size-3.5", className)} aria-label={type} />
    </span>
  );
}
