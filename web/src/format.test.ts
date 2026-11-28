import { expect, test } from "bun:test";
import { ago, formatWhen, shortID, shortSHA, shortWhen } from "./format";

test("formatWhen writes the date and time; shortWhen leaves out the year", () => {
  const f = formatWhen("2026-11-28T13:05:00Z", "en-GB", "UTC");
  expect(f).toContain("2026");
  expect(f).toContain("13:05");
  const s = shortWhen("2026-11-28T13:05:00Z", "en-GB", "UTC");
  expect(s).toContain("28 Nov");
  expect(s).toContain("13:05");
  expect(s).not.toContain("2026");
});

test("ago reads a time as minutes, hours or days ago", () => {
  const now = new Date("2026-11-28T12:00:00Z");
  expect(ago("2026-11-28T11:59:30Z", now)).toBe("just now");
  expect(ago("2026-11-28T11:15:00Z", now)).toBe("45 min ago");
  expect(ago("2026-11-28T09:00:00Z", now)).toBe("3 h ago");
  expect(ago("2026-11-27T12:00:00Z", now)).toBe("24 h ago");
  expect(ago("2026-11-20T12:00:00Z", now)).toBe("8 days ago");
  expect(ago("not a time", now)).toBe("");
});

test("M5.1: shortID keeps the end of a run ID; shortSHA the start of a commit", () => {
  expect(shortID("0198f2a4-1111-7000-8000-00000000abcd")).toBe("0000abcd");
  expect(shortSHA("d48a6d6c0ffee0000000000000000000000000ab")).toBe("d48a6d6");
});
