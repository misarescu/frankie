import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { isActive, statusLabel, statusTone } from "@/status";

// StatusBadge is a status chip in the mockup's tones and words.
export function StatusBadge({ status, label, className }: { status: string; label?: string; className?: string }) {
  const tone = statusTone(status);
  return (
    <Badge tone={tone} className={cn(status === "canceled" && "line-through", className)}>
      <span aria-hidden="true" className={cn("size-1.5 shrink-0 rounded-full bg-current", isActive(status) && "pulse-dot", tone === "outline" && "opacity-60")} />
      {label ?? statusLabel(status)}
    </Badge>
  );
}
