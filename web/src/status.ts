// How a status reads (the mockup's chips): its tone and its words. Issues,
// runs, steps, releases, stages and change requests share them.

export type Tone = "neutral" | "outline" | "primary" | "warn" | "ok" | "bad";

const tones: Record<string, Tone> = {
  draft: "neutral", refining: "primary", ready: "outline", running: "primary", queued: "primary", building_image: "primary",
  in_progress: "primary", awaiting_human: "warn", merged: "ok", succeeded: "ok", released: "ok", failed: "bad",
  rolled_back: "bad", canceled: "neutral", pending: "outline", skipped: "outline", "not started": "outline", open: "primary",
  closed: "neutral", inconclusive: "warn", expired: "bad", aborted: "bad",
};

const labels: Record<string, string> = {
  awaiting_human: "waiting for you", rolled_back: "rolled back", in_progress: "in progress", building_image: "building image",
};

export const statusTone = (s: string): Tone => tones[s] ?? "neutral";

export const statusLabel = (s: string): string => labels[s] ?? s.replaceAll("_", " ");

// isActive: the status moves on by itself; its chip's dot pulses.
export const isActive = (s: string): boolean => s === "running" || s === "refining" || s === "in_progress" || s === "building_image" || s === "queued";
