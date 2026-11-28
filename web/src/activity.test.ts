import { describe, expect, test } from "bun:test";
import { activity, sourceLabel } from "./activity";

const base = { created_at: "2026-11-24T10:00:00Z", created_by: "github-sync", runs: [] };

describe("sourceLabel", () => {
  test("a provider issue URL reads owner/repo#number", () => {
    expect(sourceLabel("https://github.com/misarescu/Goodreads-Clone/issues/162")).toBe("misarescu/Goodreads-Clone#162");
    expect(sourceLabel("https://gitlab.com/group/sub/repo/-/issues/4")).toBe("group/sub/repo#4");
  });
  test("anything else is shown as it is, without the scheme", () => {
    expect(sourceLabel("https://example.invalid/tracker/abc")).toBe("example.invalid/tracker/abc");
    expect(sourceLabel("JIRA-12")).toBe("JIRA-12");
  });
});

describe("activity (M9 issue page)", () => {
  test("an issue that was only synced shows its sync", () => {
    expect(activity({ ...base, source_ref: "https://github.com/o/r/issues/7" })).toEqual([
      { at: base.created_at, who: "github-sync", what: "synced it from o/r#7" },
    ]);
  });

  test("an issue without a source was created", () => {
    expect(activity(base)[0]!.what).toBe("created it");
  });

  test("a start request names who asked, and the parent whose request covers it", () => {
    const own = activity({ ...base, start_requested_at: "2026-11-25T09:00:00Z", start_requested_by: "ana" });
    expect(own[1]).toEqual({ at: "2026-11-25T09:00:00Z", who: "ana", what: "asked to start it" });
    const via = activity({ ...base, start_requested_at: "2026-11-25T09:00:00Z", start_requested_by: "ana", start_requested_via: "vfy-8" });
    expect(via[1]!.what).toBe("asked to start vfy-8, which covers it");
  });

  test("spec decisions: approved by a person, sent back with feedback, or a failed refinement", () => {
    const approved = activity({ ...base, spec_approved_at: "2026-11-25T09:00:00Z", spec_approved_by: "ana" });
    expect(approved[1]).toEqual({ at: "2026-11-25T09:00:00Z", who: "ana", what: "approved the spec", tone: "ok" });
    const back = activity({ ...base, spec_rejected_at: "2026-11-25T09:00:00Z", spec_feedback: "split it (ana)" });
    expect(back[1]!.what).toBe("The spec was sent back: split it (ana)");
    const failed = activity({ ...base, spec_rejected_at: "2026-11-25T09:00:00Z" });
    expect(failed[1]!.what).toContain("held until someone asks again");
  });

  test("each run starts, and a finished one ends with its outcome; a running one has no end yet", () => {
    const runs = [
      { id: "00000000-0000-7000-8000-000000002222", status: "running", workflow: "default-build@1", created_at: "2026-11-26T10:00:00Z", started_at: "2026-11-26T10:00:05Z", cost_usd: 0 },
      { id: "00000000-0000-7000-8000-000000001111", status: "failed", workflow: "default-build@1", created_at: "2026-11-25T10:00:00Z", finished_at: "2026-11-25T10:30:00Z", cost_usd: 0.5 },
    ];
    const got = activity({ ...base, runs }).slice(1);
    expect(got).toEqual([
      { at: "2026-11-25T10:00:00Z", who: "Run 00001111", what: "started (default-build@1)", run: runs[1]!.id },
      { at: "2026-11-25T10:30:00Z", who: "Run 00001111", what: "failed", run: runs[1]!.id, tone: "bad" },
      { at: "2026-11-26T10:00:05Z", who: "Run 00002222", what: "started (default-build@1)", run: runs[0]!.id },
    ]);
  });

  test("closing says who canceled or completed it and why", () => {
    const canceled = activity({ ...base, status: "canceled", closed_at: "2026-11-27T10:00:00Z", closed_by: "ana", close_reason: "out of scope" });
    expect(canceled.at(-1)).toEqual({ at: "2026-11-27T10:00:00Z", who: "ana", what: "canceled it: out of scope", tone: undefined });
    const done = activity({ ...base, status: "merged", closed_at: "2026-11-27T10:00:00Z", closed_by: "ana" });
    expect(done.at(-1)!.what).toBe("completed it");
  });

  test("entries are in time order, oldest first, whatever order the fields come in", () => {
    const got = activity({
      ...base, status: "canceled", closed_at: "2026-11-24T12:00:00Z", closed_by: "ana",
      start_requested_at: "2026-11-24T11:00:00Z", start_requested_by: "ana",
      runs: [{ id: "r-1", status: "canceled", workflow: "w", created_at: "2026-11-24T11:01:00Z", finished_at: "2026-11-24T11:59:00Z", cost_usd: 0 }],
    });
    expect(got.map((e) => e.at)).toEqual(["2026-11-24T10:00:00Z", "2026-11-24T11:00:00Z", "2026-11-24T11:01:00Z", "2026-11-24T11:59:00Z", "2026-11-24T12:00:00Z"]);
  });
});
