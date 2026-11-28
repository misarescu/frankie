import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

const alertVariants = cva("flex items-start gap-3 rounded-lg border px-4 py-3 text-[13px] [&>svg]:mt-0.5 [&>svg]:shrink-0", {
  variants: {
    tone: {
      default: "border-line bg-card text-fg",
      warn: "border-warn/40 bg-warn-soft text-fg [&>svg]:text-warn",
      bad: "border-bad/40 bg-bad-soft text-fg [&>svg]:text-bad",
      ok: "border-ok/40 bg-ok-soft text-fg [&>svg]:text-ok",
      primary: "border-primary/40 bg-primary-soft text-fg [&>svg]:text-primary",
    },
  },
  defaultVariants: { tone: "default" },
});

export function Alert({ className, tone, ...props }: ComponentProps<"div"> & VariantProps<typeof alertVariants>) {
  return <div data-slot="alert" role="alert" className={cn(alertVariants({ tone }), className)} {...props} />;
}

export function AlertTitle({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="alert-title" className={cn("font-medium", className)} {...props} />;
}

export function AlertDescription({ className, ...props }: ComponentProps<"div">) {
  return <div data-slot="alert-description" className={cn("text-muted-fg", className)} {...props} />;
}
