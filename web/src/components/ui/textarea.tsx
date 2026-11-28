import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export function Textarea({ className, ...props }: ComponentProps<"textarea">) {
  return (
    <textarea
      data-slot="textarea"
      className={cn(
        "min-h-16 w-full min-w-0 rounded-md border border-line bg-card px-2.5 py-1.5 text-[13px] leading-relaxed placeholder:text-muted-fg disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-bad",
        className,
      )}
      {...props}
    />
  );
}
