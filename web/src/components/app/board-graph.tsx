import { useRouter } from "@tanstack/react-router";
import { GitForkIcon } from "lucide-react";
import { useId, type MouseEvent } from "react";
import { EmptyState } from "@/components/app/states";
import { Card } from "@/components/ui/card";
import { dependencyGraph, type IssueSummary } from "@/issues";
import { paths } from "@/paths";
import { statusLabel, statusTone } from "@/status";

const W = 200, H = 44, GX = 80, GY = 22, P = 16;
const stroke: Record<string, string> = { ok: "var(--ok)", bad: "var(--bad)", primary: "var(--primary)", warn: "var(--warn)" };
const clip = (s: string, n: number) => (s.length > n ? `${s.slice(0, n - 1)}…` : s);

// BoardGraph is the board's dependency view: the issues that wait for others,
// in layers, with an arrow from an issue to each one waiting for it. A node
// opens its issue.
export function BoardGraph({ issues }: { issues: IssueSummary[] }) {
  const router = useRouter();
  const arrow = `arrow-${useId().replaceAll(":", "")}`;
  const { nodes, edges } = dependencyGraph(issues);
  if (nodes.length === 0) {
    return (
      <EmptyState icon={<GitForkIcon className="size-6" />} title="No dependencies between open issues">
        An issue that waits for another (a "blocked by" link on its GitHub issue) shows here, to the right of what it waits for.
      </EmptyState>
    );
  }
  const at = new Map(nodes.map((n) => [n.key, { x: P + n.layer * (W + GX), y: P + n.row * (H + GY) }]));
  const width = P * 2 + Math.max(...nodes.map((n) => n.layer)) * (W + GX) + W;
  const height = P * 2 + Math.max(...nodes.map((n) => n.row)) * (H + GY) + H;
  const open = (key: string) => (e: MouseEvent) => {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    e.preventDefault();
    void router.navigate({ href: paths.issue(key) });
  };
  return (
    <Card className="p-2">
      <figure aria-label="issue dependency graph" className="scroll-thin overflow-x-auto">
        <svg width={width} height={height} className="block">
          <defs>
            <marker id={arrow} viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto">
              <path d="M0 0L10 5L0 10z" fill="var(--muted-fg)" />
            </marker>
          </defs>
          {edges.map((e) => {
            const dep = at.get(e.to)!, waiter = at.get(e.from)!;
            const x1 = dep.x + W, y1 = dep.y + H / 2, x2 = waiter.x - 6, y2 = waiter.y + H / 2;
            return (
              <path key={`${e.from}>${e.to}`} d={`M${x1} ${y1} C${x1 + GX / 2} ${y1} ${x2 - GX / 2} ${y2} ${x2} ${y2}`}
                fill="none" stroke="var(--muted-fg)" strokeWidth="1.3" markerEnd={`url(#${arrow})`} />
            );
          })}
          {nodes.map((n) => {
            const p = at.get(n.key)!;
            return (
              <a key={n.key} href={paths.issue(n.key)} onClick={open(n.key)} aria-label={`${n.key} ${n.title}, ${statusLabel(n.status)}`} className="cursor-pointer [&:hover_rect]:fill-[var(--hover)]">
                <title>{`${n.key} ${n.title} (${statusLabel(n.status)})`}</title>
                <rect x={p.x} y={p.y} width={W} height={H} rx="8" fill="var(--card)" stroke={stroke[statusTone(n.status)] ?? "var(--border)"} strokeWidth="1.5" />
                <text x={p.x + 12} y={p.y + 18} fill="var(--muted-fg)" fontFamily="var(--font-mono)" fontSize="11">{clip(`${n.key} · ${statusLabel(n.status)}`, 30)}</text>
                <text x={p.x + 12} y={p.y + 34} fill="var(--fg)" fontSize="12.5">{clip(n.title, 28)}</text>
              </a>
            );
          })}
        </svg>
      </figure>
      <p className="px-2 pt-1 pb-1 text-xs text-muted-fg">An arrow points from an issue to the one that waits for it. Click an issue to open it.</p>
    </Card>
  );
}
