// How the UI writes times and ids: one place, so every page reads them alike.

// formatWhen is a time with its date: "28 Nov 2026, 13:05" (in the browser's locale).
export const formatWhen = (iso: string, locale?: string, timeZone?: string): string =>
  new Date(iso).toLocaleString(locale, { dateStyle: "medium", timeStyle: "short", timeZone });

// shortWhen is a time in a narrow column, without the year: "28 Nov, 13:05".
export const shortWhen = (iso: string, locale?: string, timeZone?: string): string =>
  new Date(iso).toLocaleString(locale, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", timeZone });

// ago says how long ago a time was, as people read it in a list.
export function ago(iso: string, now: Date = new Date()): string {
  const s = Math.round((now.getTime() - Date.parse(iso)) / 1000);
  if (!Number.isFinite(s)) return "";
  if (s < 60) return "just now";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h} h ago`;
  return `${Math.floor(h / 24)} days ago`;
}

// shortID shortens a run's ID for lists. IDs are UUIDv7, whose leading digits
// are a timestamp shared by runs started close together, so it keeps the end.
export const shortID = (id: string): string => id.slice(-8);

// shortSHA is a commit as people read it.
export const shortSHA = (sha: string): string => sha.slice(0, 7);
