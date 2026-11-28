// Writes (D141, D146): one hook per write the UI makes, each sending the
// browser session cookie (apiFetch) and invalidating what it changes. The
// control plane records the authenticated user's identity from the session.

import { useMutation, useQueryClient, type QueryClient, type QueryKey } from "@tanstack/react-query";
import { decide, type Decision } from "@/approvals";
import { apiFetch, type APIToken, type Invite, type Role, type User } from "@/auth";
import { cancelIssue, completeIssue, retryIssue, startIssues } from "@/issues";
import { answer } from "@/questions";
import { decideSpec, type SpecDecision } from "@/specs";
import { abort, steer } from "@/steps";
import { keys } from "./queries";

export type Write = "start" | "retry" | "complete" | "cancel" | "decide-gate" | "answer" | "decide-spec" | "steer" | "abort";

// staleAfter lists what each write changes, as query key prefixes.
export function staleAfter(w: Write, key?: string): QueryKey[] {
  const issue: QueryKey[] = key ? [keys.issue(key)] : [["issue"]];
  switch (w) {
    case "start":
    case "retry":
    case "complete":
    case "cancel":
      return [["issues"], ["issue"], keys.runs, keys.specs];
    case "decide-gate":
      return [keys.approvals, ["issue"], ["issues"], keys.runs, ["run"], ["release"], ["releases"]];
    case "answer":
      return [keys.questions, ["issue"], ["run"]];
    case "decide-spec":
      return [keys.specs, ...issue, ["issues"]];
    case "steer":
    case "abort":
      return [["run"], ["issue"], keys.runs];
  }
}

// The workflow records a decision or an answer right after the signal the
// API answered 202 for, so the inbox refreshes once more a moment later.
const SETTLE_MS = 1500;

function refresh(qc: QueryClient, stale: QueryKey[], again = false) {
  const run = () => { for (const k of stale) void qc.invalidateQueries({ queryKey: k }); };
  run();
  if (again) setTimeout(run, SETTLE_MS);
}

async function jsonRequest<T>(fetchFn: typeof apiFetch, url: string, method: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: "same-origin" };
  if (body !== undefined) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  const r = await fetchFn(url, init);
  if (!r.ok) throw new Error((await r.text()).trim() || `status ${r.status}`);
  if (r.status === 204) return undefined as T;
  return r.json() as Promise<T>;
}

// ---- Auth & self-service account ----

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (v: { username: string; password: string }) => {
      const r = await fetch("/api/auth/login", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(v),
      });
      if (!r.ok) throw new Error((await r.text()).trim() || `status ${r.status}`);
      return (await r.json()) as { user: User };
    },
    onSuccess: ({ user }) => {
      qc.setQueryData(keys.me, user);
    },
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => {
      const r = await fetch("/api/auth/logout", { method: "POST", credentials: "same-origin" });
      if (!r.ok && r.status !== 204) throw new Error((await r.text()).trim() || `status ${r.status}`);
    },
    onSuccess: () => {
      qc.clear();
      qc.setQueryData(keys.me, null);
    },
  });
}

export function useAcceptInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (v: { token: string; username: string; display_name: string; email: string; password: string }) => {
      const r = await fetch("/api/auth/invites/accept", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(v),
      });
      if (!r.ok) throw new Error((await r.text()).trim() || `status ${r.status}`);
      return (await r.json()) as { user: User };
    },
    onSuccess: ({ user }) => {
      qc.setQueryData(keys.me, user);
    },
  });
}

export function useUpdateProfile() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { display_name: string; email: string }) =>
      jsonRequest<{ user: User }>(apiFetch, "/api/auth/me", "PATCH", v),
    onSuccess: ({ user }) => {
      qc.setQueryData(keys.me, user);
    },
  });
}

export function useChangePassword() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { current_password: string; new_password: string }) =>
      jsonRequest<void>(apiFetch, "/api/auth/password", "POST", v),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.me }),
  });
}

export function useUnlinkIdentity() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      jsonRequest<void>(apiFetch, `/api/auth/identities/${encodeURIComponent(id)}`, "DELETE"),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.me }),
  });
}

export function useCreateToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { name: string; expires_in_days?: number }) =>
      jsonRequest<{ token: APIToken; secret: string }>(apiFetch, "/api/tokens", "POST", v),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.tokens }),
  });
}

export function useRevokeToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      jsonRequest<void>(apiFetch, `/api/tokens/${encodeURIComponent(id)}`, "DELETE"),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.tokens }),
  });
}

// ---- Admin users & invites ----

export function useCreateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { username: string; display_name: string; email: string; role: Role; password: string }) =>
      jsonRequest<User>(apiFetch, "/api/users", "POST", v),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.users }),
  });
}

export function useUpdateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { id: string; role?: Role; disabled?: boolean }) =>
      jsonRequest<User>(apiFetch, `/api/users/${encodeURIComponent(v.id)}`, "PATCH", {
        ...(v.role !== undefined ? { role: v.role } : {}),
        ...(v.disabled !== undefined ? { disabled: v.disabled } : {}),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.users });
      void qc.invalidateQueries({ queryKey: keys.me });
    },
  });
}

export function useRevokeUserSessions() {
  return useMutation({
    mutationFn: (id: string) =>
      jsonRequest<void>(apiFetch, `/api/users/${encodeURIComponent(id)}/sessions`, "DELETE"),
  });
}

export function useCreateInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { email?: string; role: Role; expires_in_hours?: number }) =>
      jsonRequest<{ invite: Invite; token: string; path: string }>(apiFetch, "/api/invites", "POST", v),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.invites }),
  });
}

export function useDeleteInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      jsonRequest<void>(apiFetch, `/api/invites/${encodeURIComponent(id)}`, "DELETE"),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.invites }),
  });
}

// ---- Operational writes (member+) ----

export function useStartIssues() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (issueKeys: string[]) => startIssues(apiFetch, issueKeys),
    onSuccess: () => refresh(qc, staleAfter("start")),
  });
}

export function useRetryIssue() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (key: string) => retryIssue(apiFetch, key),
    onSuccess: () => refresh(qc, staleAfter("retry")),
  });
}

export function useCompleteIssue() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (key: string) => completeIssue(apiFetch, key),
    onSuccess: () => refresh(qc, staleAfter("complete")),
  });
}

export function useCancelIssue() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { key: string; reason: string }) => cancelIssue(apiFetch, v.key, v.reason),
    onSuccess: () => refresh(qc, staleAfter("cancel")),
  });
}

export function useDecideGate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { gateID: string; decision: Decision; comment: string }) => decide(apiFetch, v.gateID, v.decision, v.comment),
    onSuccess: () => refresh(qc, staleAfter("decide-gate"), true),
  });
}

export function useAnswerQuestion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { questionID: string; text: string }) => answer(apiFetch, v.questionID, v.text),
    onSuccess: () => refresh(qc, staleAfter("answer"), true),
  });
}

export function useDecideSpec() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { key: string; decision: SpecDecision; comment: string }) => decideSpec(apiFetch, v.key, v.decision, v.comment),
    onSuccess: (_, v) => refresh(qc, staleAfter("decide-spec", v.key)),
  });
}

export function useSteer() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { stepID: string; text: string }) => steer(apiFetch, v.stepID, v.text),
    onSuccess: () => refresh(qc, staleAfter("steer"), true),
  });
}

export function useAbort() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (v: { stepID: string; reason: string }) => abort(apiFetch, v.stepID, v.reason),
    onSuccess: () => refresh(qc, staleAfter("abort"), true),
  });
}
