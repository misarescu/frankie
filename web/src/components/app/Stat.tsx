import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

// Stat is one figure of a page's summary strip.
export function Stat({ label, value, sub, tone }: { label: string; value: ReactNode; sub?: ReactNode; tone?: "warn" | "primary" | "bad" | "ok" }) {
  return (
    <div className="min-w-0 px-4 py-3">
      <div className="text-xs2 font-medium tracking-wider text-muted-fg uppercase">{label}</div>
      <div className={cn("tnum mt-0.5 text-lg font-semibold", tone && { warn: "text-warn", primary: "text-primary", bad: "text-bad", ok: "text-ok" }[tone])}>{value}</div>
      {sub && <div className="truncate text-xs text-muted-fg">{sub}</div>}
    </div>
  );
}
