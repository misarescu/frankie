import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export const badgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs2 font-medium whitespace-nowrap [&_svg]:shrink-0",
  {
    variants: {
      tone: {
        neutral: "bg-muted text-muted-fg",
        outline: "border border-line text-muted-fg",
        primary: "bg-primary-soft text-primary",
        warn: "bg-warn-soft text-warn",
        ok: "bg-ok-soft text-ok",
        bad: "bg-bad-soft text-bad",
        solid: "bg-warn text-bg font-semibold",
      },
    },
    defaultVariants: { tone: "neutral" },
  },
);

export type BadgeTone = NonNullable<VariantProps<typeof badgeVariants>["tone"]>;

export function Badge({ className, tone, ...props }: ComponentProps<"span"> & VariantProps<typeof badgeVariants>) {
  return <span data-slot="badge" className={cn(badgeVariants({ tone }), className)} {...props} />;
}
