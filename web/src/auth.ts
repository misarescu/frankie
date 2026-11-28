// Authentication types, role helpers and session-backed fetch (M10, D144-D147).
// Browser requests carry the HttpOnly `frankie_session` cookie automatically
// (same-origin); no token is stored in localStorage.

export type Role = "admin" | "member" | "viewer";
export type SSOProvider = "google" | "gitlab" | "github";

export type SSOProviderInfo = { id: SSOProvider; name: string };

export type AuthProviders = {
  password: boolean;
  sso: SSOProviderInfo[];
};

export type UserIdentity = {
  id: string;
  provider: SSOProvider;
  email?: string;
  login?: string;
  created_at: string;
};

export type User = {
  id: string;
  username: string;
  display_name: string;
  email?: string;
  role: Role;
  has_password: boolean;
  disabled_at?: string;
  created_at: string;
  identities?: UserIdentity[];
};

export type APIToken = {
  id: string;
  name: string;
  token_prefix: string;
  last_used_at?: string;
  expires_at?: string;
  revoked_at?: string;
  created_at: string;
};

export type Invite = {
  id: string;
  email?: string;
  role: Role;
  invited_by?: string;
  expires_at: string;
  accepted_at?: string;
  created_at: string;
};

export type InvitePreview = {
  email?: string;
  role: Role;
  expires_at: string;
};

type Fetch = (input: string, init?: RequestInit) => Promise<Response>;

// Fired on window when an authenticated API call returns 401 so the shell
// redirects back to /login.
export const AUTH_UNAUTHORIZED = "frankie:unauthorized";

export function notifyUnauthorized() {
  if (typeof window !== "undefined") {
    window.dispatchEvent(new Event(AUTH_UNAUTHORIZED));
  }
}

export function canOperate(role: Role | undefined): boolean {
  return role === "member" || role === "admin";
}

export function isAdmin(role: Role | undefined): boolean {
  return role === "admin";
}

export function roleLabel(role: Role): string {
  switch (role) {
    case "admin": return "Admin";
    case "member": return "Member";
    case "viewer": return "Viewer";
  }
}

export function displayUserName(user: Pick<User, "display_name" | "username">): string {
  return user.display_name.trim() || user.username;
}

// safeNextPath ensures ?next= stays on this origin and never loops onto auth pages.
export function safeNextPath(raw: string | undefined): string {
  if (!raw) return "/";
  const trimmed = raw.trim();
  if (!trimmed.startsWith("/") || trimmed.startsWith("//") || trimmed.startsWith("/\\")) return "/";
  if (trimmed === "/login" || trimmed.startsWith("/login?") || trimmed.startsWith("/invite/")) return "/";
  return trimmed;
}

const SSO_ERRORS: Record<string, string> = {
  sso_failed: "Sign-in with the identity provider did not complete. Please try again.",
  sso_denied: "Sign-in was canceled at the identity provider.",
  invalid_state: "Your sign-in session expired before returning from the provider. Please try again.",
  missing_code: "The identity provider did not return an authorization code. Please try again.",
  not_allowed: "Your account is not invited or on the sign-in allowlist for this Frankie deployment.",
  account_disabled: "Your Frankie account has been disabled. Ask an administrator for help.",
  identity_in_use: "That external account is already linked to another Frankie user.",
  already_linked: "That external account is already linked to another Frankie user.",
  link_failed: "Could not link that external account. Please try again.",
  session_required: "Please sign in again before linking an external account.",
  invite_invalid: "That invite is invalid, expired, or restricted to a different email address.",
  provision_failed: "Could not create your Frankie account from SSO. Ask an administrator for help.",
  session_failed: "Could not start your session. Please try again.",
  provider_unavailable: "That sign-in provider is not enabled on this deployment.",
  provider_disabled: "That sign-in provider is not enabled on this deployment.",
};

export function ssoErrorMessage(code: string | undefined): string | null {
  if (!code) return null;
  return SSO_ERRORS[code] ?? "Sign-in failed. Please try again.";
}

// withSession wraps fetch for session-authenticated writes: sends same-origin
// cookies, triggers AUTH_UNAUTHORIZED on 401, and surfaces clear 403 errors.
export function withSession(fetchFn: Fetch, onUnauthorized: () => void = () => {}): Fetch {
  return async (input, init) => {
    const r = await fetchFn(input, { credentials: "same-origin", ...init });
    if (r.status === 401) {
      onUnauthorized();
      throw new Error("Your session has expired; please sign in again.");
    }
    return r;
  };
}

export const apiFetch: Fetch = withSession(
  (input, init) => fetch(input, init),
  notifyUnauthorized,
);
