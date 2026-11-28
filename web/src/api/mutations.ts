// Writes (D141): one hook per write the UI makes, each sending the API token
// (apiFetch, D58) and the person's name (signed, M5.1) through the domain
// modules, and invalidating what it changes. A failed write's error is the
// API's message, shown next to the control that made it; a 401 also opens the
// token dialog (auth.ts fires TOKEN_REJECTED).

import { useMutation, useQueryClient, type QueryClient, type QueryKey } from "@tanstack/react-query";
import { decide, type Decision } from "@/approvals";
import { apiFetch } from "@/auth";
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
