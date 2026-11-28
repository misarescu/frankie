import { Progress as ProgressPrimitive } from "radix-ui";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export function ProgressBar({ className, value, ...props }: ComponentProps<typeof ProgressPrimitive.Root>) {
  const pct = Math.max(0, Math.min(100, value ?? 0));
  return (
    <ProgressPrimitive.Root data-slot="progress" value={pct} className={cn("relative h-1.5 w-16 overflow-hidden rounded-full bg-muted", className)} {...props}>
      <ProgressPrimitive.Indicator className="h-full rounded-full bg-ok transition-transform" style={{ transform: `translateX(-${100 - pct}%)` }} />
    </ProgressPrimitive.Root>
  );
}
