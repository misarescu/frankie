// The person's name (M5.1). Until per-person identity (open question 7) the API
// takes the client's word for who acts: the UI asks for a name once, keeps it in
// this browser's localStorage like the API token, and sends it as `by` with every
// write, so decisions, answers, steers and start requests are recorded under it
// instead of "web". It is not authentication.

import type { TokenStorage } from "./auth";

const KEY = "frankie.name";

// The API refuses a longer `by` (internal/api, maxBy).
export const MAX_NAME = 100;

// Storage can be missing or throw (private windows, blocked site data); the
// name then lasts only for this page.
let memory: string | null = null;

function storage(): TokenStorage | null {
  try {
    return typeof localStorage === "undefined" ? null : localStorage;
  } catch {
    return null;
  }
}

export function loadName(s: TokenStorage | null = storage()): string | null {
  try {
    return s?.getItem(KEY) ?? memory;
  } catch {
    return memory;
  }
}

// saveName stores the trimmed name, cut to what the API accepts; an empty name
// forgets it.
export function saveName(name: string, s: TokenStorage | null = storage()): void {
  memory = name.trim().slice(0, MAX_NAME).trim() || null;
  try {
    if (memory) s?.setItem(KEY, memory);
    else s?.removeItem(KEY);
  } catch {
    // memory keeps it for this page
  }
}

// signed adds `by` to a write's body when a name is set; without one the API
// records "web".
export function signed<T extends object>(body: T, by: string | null = loadName()): T & { by?: string } {
  return by ? { ...body, by } : body;
}
