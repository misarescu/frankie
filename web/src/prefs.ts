// What the UI remembers in this browser: the theme and the last project opened (D141).

export type TokenStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;

export type Theme = "light" | "dark";

const THEME = "frankie.theme";
const PROJECT = "frankie.project";

function storage(): TokenStorage | null {
  try {
    return typeof localStorage === "undefined" ? null : localStorage;
  } catch {
    return null;
  }
}

function read(key: string, s: TokenStorage | null): string | null {
  try {
    return s?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

function store(key: string, value: string | null, s: TokenStorage | null): void {
  try {
    if (value) s?.setItem(key, value);
    else s?.removeItem(key);
  } catch {
    // blocked site data: the choice lasts for this page
  }
}

// loadTheme is the theme a person chose, or null to follow the system's.
export function loadTheme(s: TokenStorage | null = storage()): Theme | null {
  const t = read(THEME, s);
  return t === "light" || t === "dark" ? t : null;
}

export const saveTheme = (t: Theme | null, s: TokenStorage | null = storage()): void => store(THEME, t, s);

// resolveTheme is the theme to show: the chosen one, else the system's.
export const resolveTheme = (chosen: Theme | null, systemDark: boolean): Theme => chosen ?? (systemDark ? "dark" : "light");

export const loadLastProject = (s: TokenStorage | null = storage()): string | null => read(PROJECT, s);

export const saveLastProject = (prefix: string, s: TokenStorage | null = storage()): void => store(PROJECT, prefix, s);

// startProject is the project / opens: the last one, if it still exists,
// else the first.
export function startProject(prefixes: string[], last: string | null): string | undefined {
  return last && prefixes.includes(last) ? last : prefixes[0];
}
