import { useSyncExternalStore } from "react";
import { cn } from "@/lib/utils";

// A small toast (the mockup's): one message at a time, bottom right, gone
// after a few seconds. toast() can be called from anywhere, e.g. a mutation's
// onSuccess.
type Toast = { id: number; text: string; tone: "default" | "bad" };

let current: Toast | null = null;
let timer: ReturnType<typeof setTimeout> | undefined;
const listeners = new Set<() => void>();
const emit = () => { for (const l of listeners) l(); };

export function toast(text: string, opts: { tone?: Toast["tone"]; ms?: number } = {}): void {
  current = { id: (current?.id ?? 0) + 1, text, tone: opts.tone ?? "default" };
  clearTimeout(timer);
  timer = setTimeout(() => { current = null; emit(); }, opts.ms ?? 3200);
  emit();
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => { listeners.delete(l); };
}

export function Toaster() {
  const t = useSyncExternalStore(subscribe, () => current, () => null);
  return (
    <div aria-live="polite" className="pointer-events-none fixed right-4 bottom-4 z-[60] flex justify-end" style={{ marginBottom: "env(safe-area-inset-bottom, 0px)" }}>
      {t && (
        <div key={t.id} role="status" className={cn("pointer-events-auto max-w-sm rounded-md border border-line bg-card px-4 py-2.5 text-[13px] shadow-lg", t.tone === "bad" && "border-bad/40 text-bad")}>
          {t.text}
        </div>
      )}
    </div>
  );
}
