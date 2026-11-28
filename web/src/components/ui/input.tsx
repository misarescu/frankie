import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export function Input({ className, type, ...props }: ComponentProps<"input">) {
  return (
    <input
      type={type}
      data-slot="input"
      className={cn(
        "h-8 w-full min-w-0 rounded-md border border-line bg-card px-2.5 text-[13px] placeholder:text-muted-fg disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-bad",
        className,
      )}
      {...props}
    />
  );
}
