// The theme (D141): the .dark class on <html>, from the person's choice
// (prefs.ts) or else the system's, applied before the first paint and kept in
// step with the system while no choice is stored.

import { loadTheme, resolveTheme, saveTheme, type Theme } from "./prefs";

const listeners = new Set<() => void>();
const media = () => (typeof matchMedia === "undefined" ? null : matchMedia("(prefers-color-scheme: dark)"));

export const currentTheme = (): Theme => resolveTheme(loadTheme(), media()?.matches ?? false);

function apply() {
  document.documentElement.classList.toggle("dark", currentTheme() === "dark");
  for (const l of listeners) l();
}

export function initTheme(): void {
  apply();
  media()?.addEventListener("change", apply);
}

export function setTheme(t: Theme): void {
  saveTheme(t);
  apply();
}

export function subscribeTheme(l: () => void): () => void {
  listeners.add(l);
  return () => { listeners.delete(l); };
}
