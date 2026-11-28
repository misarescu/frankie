import { ToggleGroup } from "radix-ui";
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export type SegmentedOption<T extends string> = { value: T; label: ReactNode; icon?: ReactNode };

// Segmented picks one of a few views or filters (the mockup's segmented
// control), as a single-choice toggle group.
export function Segmented<T extends string>({ options, value, onValueChange, className, label }: {
  options: SegmentedOption<T>[];
  value: T;
  onValueChange: (v: T) => void;
  className?: string;
  label?: string;
}) {
  return (
    <ToggleGroup.Root
      type="single"
      value={value}
      aria-label={label}
      onValueChange={(v) => { if (v) onValueChange(v as T); }}
      className={cn("scroll-thin inline-flex max-w-full overflow-x-auto rounded-md border border-line bg-muted p-0.5", className)}
    >
      {options.map((o) => (
        <ToggleGroup.Item
          key={o.value}
          value={o.value}
          className="flex shrink-0 cursor-pointer items-center gap-1.5 rounded px-2.5 py-1 text-xs font-medium whitespace-nowrap text-muted-fg hover:text-fg data-[state=on]:bg-card data-[state=on]:text-fg data-[state=on]:shadow-sm"
        >
          {o.icon}
          {o.label}
        </ToggleGroup.Item>
      ))}
    </ToggleGroup.Root>
  );
}
