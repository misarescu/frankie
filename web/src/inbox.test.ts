import { expect, test } from "bun:test";
import type { Approval } from "./approvals";
import { emptyInbox, gateTitle, inboxCounts, inboxItems, isProtected, ofKind, releaseGateSHA, releaseOf } from "./inbox";
import type { Question } from "./questions";
import type { Spec } from "./specs";

const gate = (o: Partial<Approval>): Approval => ({
  step_run_id: "g1", run_id: "r1", step: "approve", issue_key: "vfy-6", issue_title: "Lint", reason: "Protected paths changed: .golangci.yml",
  expires_at: "2026-11-29T12:00:00Z", created_at: "2026-11-28T10:00:00Z", ...o,
});
const question = (o: Partial<Question>): Question => ({
  id: "q1", step_run_id: "s1", run_id: "r2", step: "implement", issue_key: "vfy-12", issue_title: "Emails", question: "Which sender?",
  created_at: "2026-11-28T11:00:00Z", ...o,
});
const spec = (o: Partial<Spec>): Spec => ({
  key: "vfy-5", type: "story", title: "Tags", project: "verify/demo", spec: "", acceptance_criteria: [], file_hints: [], original_spec: "", labels: [], ...o,
});

// M9 (roadmap "Approvals"): one inbox, questions first, filtered by kind, with counts per kind.
test("the inbox lists questions, then gates oldest first, then specs", () => {
  const items = inboxItems(
    [question({ id: "q2", created_at: "2026-11-28T12:00:00Z" }), question({})],
    [gate({ step_run_id: "g2", created_at: "2026-11-28T11:30:00Z" }), gate({})],
    [spec({})],
  );
  expect(items.map((i) => `${i.kind}:${i.id}`)).toEqual(["question:q1", "question:q2", "gate:g1", "gate:g2", "spec:vfy-5"]);
  expect(inboxCounts(items)).toEqual({ all: 5, gate: 2, question: 2, spec: 1 });
  expect(ofKind(items, "gate").map((i) => i.id)).toEqual(["g1", "g2"]);
  expect(ofKind(items, undefined)).toHaveLength(5);
  expect(inboxCounts([])).toEqual({ all: 0, gate: 0, question: 0, spec: 0 });
});

// D139: a gate before a release stage names its release, not an issue.
test("a release stage's gate is told from an issue's", () => {
  expect(releaseGateSHA({ issue_key: "release-fedcba9" })).toBe("fedcba9");
  expect(releaseGateSHA({ issue_key: "vfy-6" })).toBeUndefined();
  expect(releaseGateSHA({ issue_key: "release-12" })).toBeUndefined();
});

test("a release gate finds its release by its run, else by commit and repository", () => {
  const a = gate({ run_id: "r9", issue_key: "release-fedcba9", issue_title: "Release fedcba9 of verify/demo", step: "production" });
  const releases = [
    { id: "other-repo", sha: "fedcba9876", repo: "verify/other" },
    { id: "rel-1", sha: "fedcba9876", repo: "verify/demo" },
  ];
  expect(releaseOf(a, [{ ID: "r9", ReleaseID: "from-run" }], releases)).toBe("from-run");
  expect(releaseOf(a, [], releases)).toBe("rel-1");
  expect(releaseOf(a, [], [])).toBeUndefined();
  expect(releaseOf(gate({ run_id: "r9" }), [{ ID: "r9", ReleaseID: "x" }], releases)).toBeUndefined();
});

test("a gate's title says what it asks", () => {
  expect(gateTitle(gate({}))).toBe("Protected paths changed in vfy-6");
  expect(isProtected(gate({ reason: "Merge approval required (needs-merge-approval)" }))).toBe(false);
  expect(gateTitle(gate({ reason: "Merge approval required", step: "merge-approval" }))).toBe("The merge-approval gate of vfy-6");
  expect(gateTitle(gate({ issue_key: "release-fedcba9", step: "production" }))).toBe("Deploy fedcba9 to production");
  expect(gateTitle(gate({ issue_key: "release-fedcba9", step: "production" }), "v0.2.0")).toBe("Deploy v0.2.0 to production");
});

test("an empty inbox says what would appear, per kind", () => {
  expect(emptyInbox().title).toBe("Nothing waits for you");
  expect(emptyInbox("question").title).toBe("No agent waits for an answer");
  expect(emptyInbox("gate").body).toContain("release stage");
});
