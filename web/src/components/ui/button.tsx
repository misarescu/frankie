import { cva, type VariantProps } from "class-variance-authority";
import { Slot } from "radix-ui";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export const buttonVariants = cva(
  "inline-flex shrink-0 cursor-pointer items-center rounded-md font-medium whitespace-nowrap transition-colors disabled:pointer-events-none disabled:opacity-50 [&_svg]:shrink-0",
  {
    variants: {
      variant: {
        default: "bg-primary text-primary-fg hover:opacity-90",
        outline: "border border-line bg-card hover:bg-hover",
        ghost: "hover:bg-hover",
        destructive: "border border-line bg-card text-bad hover:bg-bad-soft",
        soft: "bg-primary-soft text-primary hover:opacity-90",
        link: "text-primary underline-offset-4 hover:underline",
      },
      size: {
        sm: "h-7 gap-1.5 px-2.5 text-xs",
        md: "h-8 gap-2 px-3 text-[13px]",
        icon: "size-8 justify-center",
        "icon-sm": "size-7 justify-center",
        inline: "h-auto gap-1 p-0 text-[13px]",
      },
    },
    defaultVariants: { variant: "default", size: "md" },
  },
);

export type ButtonProps = ComponentProps<"button"> & VariantProps<typeof buttonVariants> & { asChild?: boolean };

export function Button({ className, variant, size, asChild = false, type, ...props }: ButtonProps) {
  const Comp = asChild ? Slot.Root : "button";
  return <Comp data-slot="button" type={asChild ? undefined : (type ?? "button")} className={cn(buttonVariants({ variant, size }), className)} {...props} />;
}
