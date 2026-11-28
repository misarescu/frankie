import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export function Kbd({ className, ...props }: ComponentProps<"kbd">) {
  return <kbd data-slot="kbd" className={cn("mono rounded border border-line bg-muted px-1.5 text-[10px] text-muted-fg", className)} {...props} />;
}
