import { useQuery } from "@tanstack/react-query";
import {
  AlertTriangleIcon, BotIcon, ChevronRightIcon, CircleCheckIcon, CircleHelpIcon, CoinsIcon, FileDiffIcon, HammerIcon, MessageSquareIcon,
  ScrollTextIcon, SearchCheckIcon, ShieldCheckIcon, TerminalIcon, UserIcon, WrenchIcon,
} from "lucide-react";
import { useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { questionsQuery } from "@/api/queries";
import { QuestionAnswer } from "@/components/app/decisions";
import { StatusBadge } from "@/components/app/StatusBadge";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { label, summary, type RunEvent } from "@/events";
import { cn } from "@/lib/utils";
import { atBottom, diffLine, isError, isShell, rows, type Row } from "@/transcript";

const str = (v: unknown): string => (v === undefined || v === null ? "" : typeof v === "string" ? v : JSON.stringify(v, null, 2));

function when(e: RunEvent): { short: string; full: string } {
  const d = new Date(e.time);
  return { short: d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }), full: `#${e.seq} ${e.type} · ${d.toLocaleString()}` };
}

// Block is a monospace box that scrolls inside itself, for output, logs
// and diffs: long lines scroll sideways, long output down.
function Block({ children, className, tall = false }: { children: ReactNode; className?: string; tall?: boolean }) {
  return (
    <pre className={cn("mono mt-1.5 overflow-auto rounded-md bg-code p-3 text-xs leading-relaxed whitespace-pre scroll-thin", tall ? "max-h-96" : "max-h-72", className)}>
      {children}
    </pre>
  );
}

// Head is an event's label line: icon, label, extra and its time.
function Head({ icon, e, children, className }: { icon: ReactNode; e: RunEvent; children?: ReactNode; className?: string }) {
  const t = when(e);
  return (
    <div className={cn("flex min-w-0 items-center gap-2 text-xs font-medium text-muted-fg", className)}>
      <span className="shrink-0">{icon}</span>
      <span className="shrink-0">{label(e)}</span>
      <span className="min-w-0 flex-1">{children}</span>
      <time dateTime={e.time} title={t.full} className="tnum shrink-0 text-xs2 font-normal">{t.short}</time>
    </div>
  );
}

const ic = "size-3.5";

function Diff({ text }: { text: string }) {
  const tone = { add: "text-ok", del: "text-bad", hunk: "text-primary", context: "" };
  return (
    <Block tall>
      {text.split("\n").map((l, i) => <span key={i} className={cn("block", tone[diffLine(l)])}>{l || " "}</span>)}
    </Block>
  );
}

const lineTone = (l: string): string =>
  /: exit 0\b|^\S+ pass\b|: success\b/.test(l) ? "text-ok" : /: exit [1-9]|did not run|^\S+ fail\b|: failure\b|no verdict|no longer there/.test(l) ? "text-bad" : "";

const verdictStatus: Record<string, string> = { pass: "succeeded", fail: "failed", escalated: "awaiting_human" };
const verdictLabel: Record<string, string> = { pass: "passed", fail: "failed", escalated: "escalated" };

function GateResult({ e }: { e: RunEvent }) {
  const [head, ...lines] = summary(e).split("\n");
  const v = str(e.payload.verdict);
  return (
    <div className="px-4 py-3">
      <Head e={e} icon={<ShieldCheckIcon className={ic} />}>
        <span className="flex min-w-0 items-center justify-between gap-2">
          <span className="mono truncate text-fg">{head}</span>
          <StatusBadge status={verdictStatus[v] ?? v} label={verdictLabel[v] ?? v} />
        </span>
      </Head>
      {lines.length > 0 && (
        <Block className="whitespace-pre-wrap [overflow-wrap:anywhere]">
          {lines.map((l, i) => <span key={i} className={cn("block", lineTone(l))}>{l}</span>)}
        </Block>
      )}
    </div>
  );
}

// ToolRow is a tool call, folded to its tool and input; open, it shows the
// whole input and the result. A shell command's output shows open.
function ToolRow({ row }: { row: Extract<Row, { kind: "tool" }> }) {
  const shell = isShell(row);
  const [open, setOpen] = useState(shell);
  const input = row.call.payload.input;
  const first = summary(row.call);
  const whole = str(input);
  const failed = isError(row.result);
  const result = row.result ? (row.result.type === "command_output" ? str(row.result.payload.output) : str(row.result.payload.content)) : "";
  const t = when(row.call);
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="px-4 py-2">
      <CollapsibleTrigger className="flex w-full min-w-0 cursor-pointer items-center gap-2 text-left text-[12.5px]">
        <ChevronRightIcon aria-hidden="true" className={cn("size-3 shrink-0 text-muted-fg transition-transform", open && "rotate-90")} />
        {shell ? <TerminalIcon aria-hidden="true" className={cn(ic, "shrink-0 text-muted-fg")} /> : <WrenchIcon aria-hidden="true" className={cn(ic, "shrink-0 text-muted-fg")} />}
        <span className="shrink-0 rounded bg-muted px-1.5 font-medium">{str(row.call.payload.name) || "tool"}</span>
        <span className="mono min-w-0 flex-1 truncate text-xs text-muted-fg">{first.split("\n")[0]}</span>
        {!row.result && <span className="shrink-0 text-xs2 text-muted-fg">no result yet</span>}
        {failed && <span className="shrink-0 text-xs2 font-medium text-bad">error</span>}
        <time dateTime={row.call.time} title={t.full} className="tnum shrink-0 text-xs2 text-muted-fg">{t.short}</time>
      </CollapsibleTrigger>
      <CollapsibleContent>
        {shell ? (first.includes("\n") || first.length > 100) && <Block className="whitespace-pre-wrap [overflow-wrap:anywhere]">{first}</Block>
          : whole !== "" && <Block>{whole}</Block>}
        {row.result && (
          <>
            <div className="mt-2 text-xs2 font-medium tracking-wider text-muted-fg uppercase">{row.result.type === "command_output" ? "Output" : "Result"}</div>
            <Block tall className={cn(failed && "text-bad")}>{result || "(empty)"}</Block>
          </>
        )}
      </CollapsibleContent>
    </Collapsible>
  );
}

function PendingAnswer({ e }: { e: RunEvent }) {
  const questions = useQuery(questionsQuery()).data ?? [];
  const q = questions.find((x) => x.step_run_id === e.step_run_id && x.question === str(e.payload.question));
  if (!q) return null;
  return (
    <div className="mt-2">
      <QuestionAnswer question={q} />
    </div>
  );
}

function EventRow({ e }: { e: RunEvent }) {
  const text = summary(e);
  switch (e.type) {
    case "assistant_message":
      return (
        <div className="px-4 py-3">
          <Head e={e} icon={<BotIcon className={ic} />} />
          <p className="mt-1 max-w-[80ch] text-[13px] leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]">{text}</p>
        </div>
      );
    case "command_output":
    case "tool_result":
      return (
        <div className="px-4 py-2">
          <Head e={e} icon={<TerminalIcon className={ic} />} />
          <Block tall className={cn(isError(e) && "text-bad")}>{text || "(empty)"}</Block>
        </div>
      );
    case "file_diff":
      return (
        <div className="px-4 py-2">
          <Head e={e} icon={<FileDiffIcon className={ic} />}><span className="mono block truncate text-fg">{str(e.payload.path)}</span></Head>
          <Diff text={str(e.payload.diff)} />
        </div>
      );
    case "build_log":
      return (
        <div className="px-4 py-2">
          <Head e={e} icon={<ScrollTextIcon className={ic} />} />
          <Block tall>{text}</Block>
        </div>
      );
    case "gate_result":
      return <GateResult e={e} />;
    case "error":
      return (
        <div role="alert" className="bg-bad-soft px-4 py-3">
          <Head e={e} icon={<AlertTriangleIcon className={ic} />} className="text-bad" />
          <p className="mt-1 text-[13px] whitespace-pre-wrap text-bad [overflow-wrap:anywhere]">{text}</p>
        </div>
      );
    case "question":
      return (
        <div className="bg-warn-soft px-4 py-3">
          <Head e={e} icon={<CircleHelpIcon className={ic} />} className="text-warn" />
          <p className="mt-1 text-[13px] whitespace-pre-wrap [overflow-wrap:anywhere]">{str(e.payload.question)}</p>
          <PendingAnswer e={e} />
        </div>
      );
    case "intervention":
      return (
        <div className={cn("px-4 py-3", e.payload.action === "abort" ? "bg-bad-soft" : "bg-primary-soft")}>
          <Head e={e} icon={<UserIcon className={ic} />} />
          <p className="mt-1 text-[13px] whitespace-pre-wrap [overflow-wrap:anywhere]">{text}</p>
        </div>
      );
    case "findings":
      return (
        <div className="px-4 py-3">
          <Head e={e} icon={<SearchCheckIcon className={ic} />} />
          <p className="mt-1 text-[13px] whitespace-pre-wrap [overflow-wrap:anywhere]">{text}</p>
        </div>
      );
    case "image_build":
      return <Line e={e} icon={<HammerIcon className={ic} />} text={text} tone={e.payload.status === "failed" ? "text-bad" : undefined} />;
    case "token_usage":
      return <Line e={e} icon={<CoinsIcon className={ic} />} text={text} />;
    case "done":
      return <Line e={e} icon={<CircleCheckIcon className={ic} />} text={text} tone={e.payload.status === "failed" ? "text-bad" : undefined} />;
    case "state_change":
      return <Line e={e} icon={<ChevronRightIcon className={ic} />} text={text} />;
    default:
      return (
        <div className="px-4 py-2">
          <Head e={e} icon={<MessageSquareIcon className={ic} />} />
          <Block className="whitespace-pre-wrap [overflow-wrap:anywhere]">{text}</Block>
        </div>
      );
  }
}

// Line is a one-line event: a status, usage, an image build, the session's end.
function Line({ e, icon, text, tone }: { e: RunEvent; icon: ReactNode; text: string; tone?: string }) {
  const t = when(e);
  return (
    <div className="flex min-w-0 items-start gap-2 px-4 py-1.5 text-xs text-muted-fg">
      <span className="mt-0.5 shrink-0">{icon}</span>
      <span className={cn("min-w-0 flex-1 [overflow-wrap:anywhere]", tone)}><span className="font-medium">{label(e)}</span> · {text}</span>
      <time dateTime={e.time} title={t.full} className="tnum shrink-0 text-xs2">{t.short}</time>
    </div>
  );
}

// Transcript is the selected step's events in plain words (D67), following
// the end while it is live unless the person scrolled up.
export function Transcript({ events, follow, empty }: { events: RunEvent[]; follow: boolean; empty: ReactNode }) {
  const box = useRef<HTMLDivElement>(null);
  const stick = useRef(follow);
  const list = useMemo(() => rows(events), [events]);
  const [first] = events;
  const startKey = first ? `${first.step_run_id ?? "run"}:${first.seq}` : "";
  // A step opened while it runs starts at its end; a finished one at its start.
  useLayoutEffect(() => {
    const el = box.current;
    stick.current = follow;
    if (el) el.scrollTop = follow ? el.scrollHeight : 0;
  }, [startKey, follow]);
  useLayoutEffect(() => {
    const el = box.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [list.length]);
  if (events.length === 0) return <div className="px-4 py-10 text-center text-[13px] text-muted-fg">{empty}</div>;
  return (
    <div ref={box} role="log" aria-label="Transcript" className="max-h-[70vh] divide-y divide-line overflow-y-auto scroll-thin"
      onScroll={(ev) => {
        const el = ev.currentTarget;
        stick.current = atBottom(el.scrollTop, el.clientHeight, el.scrollHeight);
      }}>
      {list.map((r) => (r.kind === "tool" ? <ToolRow key={r.call.seq} row={r} /> : <EventRow key={r.event.seq} e={r.event} />))}
    </div>
  );
}
