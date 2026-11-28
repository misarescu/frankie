import { ProgressBar } from "@/components/ui/progress";
import { progress } from "@/issues";

// IssueProgress shows how far a parent is: its finished leaves (merged,
// released or canceled) of all.
export function IssueProgress({ statuses }: { statuses: string[] }) {
  const p = progress(statuses);
  const done = p.merged + p.canceled;
  const pct = p.total ? Math.round((100 * done) / p.total) : 0;
  return (
    <div className="flex items-center gap-2" title={`${done} of ${p.total} done${p.canceled ? `, ${p.canceled} canceled` : ""}`}>
      <ProgressBar value={pct} aria-label={`${done} of ${p.total} done`} />
      <span className="tnum text-xs2 text-muted-fg">{done}/{p.total}</span>
    </div>
  );
}
