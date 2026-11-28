import { BotIcon, GitMergeIcon, PlayIcon, RepeatIcon, ShieldCheckIcon } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { AppLink } from "@/components/app/AppLink";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { duration } from "@/live";
import { cn } from "@/lib/utils";
import { paths } from "@/paths";
import { statusTone } from "@/status";
import { shownStatus, stepName, usd, waitNote, who, type Step } from "@/steps";
import { loopBack, RUN_SECTION } from "@/transcript";

// useNow is the time, ticking every second while active, so a running
// step's duration counts up.
export function useNow(active: boolean): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(t);
  }, [active]);
  return now;
}

const toneText: Record<string, string> = { ok: "text-ok", bad: "text-bad", primary: "text-primary", warn: "text-warn" };

export function StepIcon({ step, className }: { step: Pick<Step, "kind" | "status">; className?: string }) {
  const Icon = step.kind === "agent" ? BotIcon : step.kind === "gate" ? ShieldCheckIcon : GitMergeIcon;
  return <Icon aria-hidden="true" className={cn("size-4 shrink-0", toneText[statusTone(step.status)] ?? "text-muted-fg", className)} />;
}

// stepFacts is what the step list and the transcript's header say about a
// step: who does it, how long it took and what it cost.
export function stepFacts(s: Step, now: Date): string {
  return [who(s), duration(s.started_at, s.finished_at, now), s.cost_usd > 0 ? usd(s.cost_usd) : ""].filter(Boolean).join(" · ");
}

// StepNotes are the lines under a step: why it ran again, why it was
// skipped, and what it waits for.
export function StepNotes({ timeline, index, link = true }: { timeline: Step[]; index: number; link?: boolean }) {
  const s = timeline[index]!;
  const loop = loopBack(timeline, index);
  const wait = waitNote(s);
  return (
    <>
      {loop && (
        <span className="mt-1 flex items-center gap-1 text-xs text-bad">
          <RepeatIcon aria-hidden="true" className="size-3 shrink-0" />{loop}
        </span>
      )}
      {s.status === "skipped" && s.skipped_because && <span className="block text-xs text-muted-fg">skipped: {s.skipped_because}</span>}
      {s.status === "not started" && <span className="block text-xs text-muted-fg">not reached yet</span>}
      {wait && (
        <span className="block text-xs text-warn">
          {wait.href && link ? <AppLink href={wait.href} className="underline underline-offset-2">{wait.text}</AppLink> : wait.text}
        </span>
      )}
    </>
  );
}

// StepList is the run's steps (D73, D98) as links that select one: what
// ran, in order, the skipped steps where they would have run, and, while the
// run goes, the steps not reached yet, greyed.
export function StepList({ runID, timeline, selected, runEvents }: { runID: string; timeline: Step[]; selected?: string; runEvents: number }) {
  const now = useNow(timeline.some((s) => s.status === "running"));
  return (
    <Card className="hidden h-fit overflow-hidden lg:block">
      <CardHeader className="flex-col items-start gap-0">
        <CardTitle>Steps</CardTitle>
        <CardDescription>the workflow snapshotted at start</CardDescription>
      </CardHeader>
      <ol className="py-1">
        {runEvents > 0 && (
          <li>
            <AppLink href={paths.run(runID, RUN_SECTION)} aria-current={selected === RUN_SECTION ? "true" : undefined}
              className={cn("flex w-full items-start gap-3 px-4 py-2 text-left hover:bg-hover", selected === RUN_SECTION && "bg-primary-soft")}>
              <PlayIcon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-fg" />
              <span className="min-w-0 flex-1">
                <span className="block text-[12.5px] font-medium">Run</span>
                <span className="block text-xs text-muted-fg">{runEvents} {runEvents === 1 ? "event" : "events"} of the run and its issue</span>
              </span>
            </AppLink>
          </li>
        )}
        {timeline.map((s, i) => (
          <li key={s.id}>
            <AppLink href={paths.run(runID, s.id)} aria-current={s.id === selected ? "true" : undefined}
              className={cn("flex w-full items-start gap-3 px-4 py-2 text-left hover:bg-hover", s.id === selected && "bg-primary-soft",
                (s.status === "not started" || s.status === "skipped") && s.id !== selected && "opacity-60")}>
              <StepIcon step={s} className="mt-0.5" />
              <span className="min-w-0 flex-1">
                <span className="flex items-center justify-between gap-2">
                  <span className="mono min-w-0 text-[12.5px] font-medium [overflow-wrap:anywhere]">{stepName(s)}</span>
                  <StatusBadge status={shownStatus(s)} />
                </span>
                <span className="block truncate text-xs text-muted-fg">{stepFacts(s, now)}</span>
                <StepNotes timeline={timeline} index={i} link={false} />
              </span>
            </AppLink>
          </li>
        ))}
      </ol>
    </Card>
  );
}

// StepPills is the step list at phone width: a row of chips above the
// transcript, scrolling sideways inside its own box.
export function StepPills({ runID, timeline, selected, runEvents }: { runID: string; timeline: Step[]; selected?: string; runEvents: number }) {
  const pill = "flex shrink-0 items-center gap-1.5 rounded-md border bg-card px-2 py-1 text-xs";
  const ring: Record<string, string> = { ok: "border-ok", bad: "border-bad", primary: "border-primary", warn: "border-warn" };
  const box = useRef<HTMLElement>(null);
  // The selected step's chip scrolls into the row's middle.
  useLayoutEffect(() => {
    const nav = box.current;
    const el = nav?.querySelector<HTMLElement>('[aria-current="true"]');
    if (nav && el) nav.scrollLeft = el.offsetLeft - (nav.clientWidth - el.offsetWidth) / 2;
  }, [selected]);
  return (
    <nav ref={box} aria-label="Steps" className="relative -mx-1 flex gap-1.5 overflow-x-auto px-1 py-1 scroll-thin lg:hidden">
      {runEvents > 0 && (
        <AppLink href={paths.run(runID, RUN_SECTION)} aria-current={selected === RUN_SECTION ? "true" : undefined}
          className={cn(pill, "border-line", selected === RUN_SECTION && "ring-2 ring-ring")}>
          <PlayIcon aria-hidden="true" className="size-3.5 text-muted-fg" />Run
        </AppLink>
      )}
      {timeline.map((s) => {
        const st = shownStatus(s);
        return (
          <AppLink key={s.id} href={paths.run(runID, s.id)} aria-current={s.id === selected ? "true" : undefined}
            className={cn(pill, ring[statusTone(st)] ?? "border-line", s.status === "skipped" && "border-dashed opacity-60",
              s.status === "not started" && "opacity-60", s.id === selected && "opacity-100 ring-2 ring-ring")}>
            <StepIcon step={s} className="size-3.5" />
            <span className="mono">{s.step_id}{s.attempt > 1 && <span className="text-muted-fg"> #{s.attempt}</span>}</span>
            {st === "running" && <span aria-hidden="true" className="pulse-dot size-1.5 rounded-full bg-primary" />}
          </AppLink>
        );
      })}
    </nav>
  );
}
