// The API token (D58): every write request carries it as a bearer token. The
// UI asks for it once and keeps it in this browser's localStorage; reads need
// no token.

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

// A minimal Storage, so tests can pass a Map-backed one.
export type TokenStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">;

const KEY = "frankie.apiToken";

// Fired on window when the API rejects the token, so the token bar can ask again.
export const TOKEN_REJECTED = "frankie:token-rejected";

// Storage can be missing or throw (private windows, blocked site data); the
// token then lasts only for this page.
let memory: string | null = null;

function storage(): TokenStorage | null {
  try {
    return typeof localStorage === "undefined" ? null : localStorage;
  } catch {
    return null;
  }
}

export function loadToken(s: TokenStorage | null = storage()): string | null {
  try {
    return s?.getItem(KEY) ?? memory;
  } catch {
    return memory;
  }
}

export function saveToken(token: string, s: TokenStorage | null = storage()): void {
  memory = token.trim() || null;
  try {
    if (memory) s?.setItem(KEY, memory);
    else s?.removeItem(KEY);
  } catch {
    // memory keeps it for this page
  }
}

export function clearToken(s: TokenStorage | null = storage()): void {
  saveToken("", s);
}

// withToken wraps fetch so each request carries the stored token. A 401 means
// the token is missing or wrong: it notifies the page and fails with a message
// saying what to do.
export function withToken(fetchFn: Fetch, token: () => string | null, onRejected: () => void = () => {}): Fetch {
  return async (input, init) => {
    const headers = new Headers(init?.headers);
    const t = token();
    if (t) headers.set("Authorization", `Bearer ${t}`);
    const r = await fetchFn(input, { ...init, headers });
    if (r.status === 401) {
      onRejected();
      throw new Error(t ? "the API token was rejected; set the right one at the top of the page"
        : "this needs the API token; set it at the top of the page");
    }
    return r;
  };
}

// apiFetch is fetch with the browser's stored token, for write requests.
export const apiFetch: Fetch = withToken(
  (input, init) => fetch(input, init),
  () => loadToken(),
  () => window.dispatchEvent(new Event(TOKEN_REJECTED)),
);
