import { usd } from "@/steps";
import { cn } from "@/lib/utils";

// Cost shows what an issue spent on models (D142): a parent its total with
// its own cost beneath; a leaf its own; nothing spent as a dash.
export function Cost({ own, total, parent = false, className }: { own: number; total?: number; parent?: boolean; className?: string }) {
  if (parent) {
    const all = total ?? own;
    return (
      <span className={cn("tnum", className)} title={`own ${usd(own)} + ${usd(Math.max(0, all - own))} from the issues under it`}>
        <span className="font-medium">{usd(all)}</span>
        <span className="block text-xs2 text-muted-fg">own {usd(own)}</span>
      </span>
    );
  }
  return own > 0 ? <span className={cn("tnum", className)}>{usd(own)}</span> : <span className={cn("text-muted-fg", className)}>-</span>;
}
