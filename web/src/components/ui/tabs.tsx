import { Tabs as TabsPrimitive } from "radix-ui";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

export function Tabs({ className, ...props }: ComponentProps<typeof TabsPrimitive.Root>) {
  return <TabsPrimitive.Root data-slot="tabs" className={cn("flex flex-col gap-4", className)} {...props} />;
}

export function TabsList({ className, ...props }: ComponentProps<typeof TabsPrimitive.List>) {
  return <TabsPrimitive.List data-slot="tabs-list" className={cn("scroll-thin flex gap-1 overflow-x-auto border-b border-line", className)} {...props} />;
}

export function TabsTrigger({ className, ...props }: ComponentProps<typeof TabsPrimitive.Trigger>) {
  return (
    <TabsPrimitive.Trigger
      data-slot="tabs-trigger"
      className={cn(
        "-mb-px flex shrink-0 cursor-pointer items-center gap-1.5 border-b-2 border-transparent px-3 py-2 text-[13px] font-medium text-muted-fg transition-colors hover:text-fg data-[state=active]:border-primary data-[state=active]:text-fg",
        className,
      )}
      {...props}
    />
  );
}

export function TabsContent({ className, ...props }: ComponentProps<typeof TabsPrimitive.Content>) {
  return <TabsPrimitive.Content data-slot="tabs-content" className={cn("min-w-0 outline-none", className)} {...props} />;
}

// TabCount is the count a tab shows beside its label.
export function TabCount({ className, ...props }: ComponentProps<"span">) {
  return <span className={cn("tnum rounded-full bg-muted px-1.5 text-xs2 text-muted-fg", className)} {...props} />;
}
