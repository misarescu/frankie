import { expect, test } from "bun:test";
import { isLive, notesSourceText, releaseName, shortSHA, verificationState, verificationText, versionText } from "./releases";

const sha = "d48a6d6c0ffee0000000000000000000000000ab";

// M7, D129, D130.
test("a release is named by its version, else its commit", () => {
  expect(shortSHA(sha)).toBe("d48a6d6");
  expect(releaseName({ sha })).toBe("d48a6d6");
  expect(releaseName({ sha, version: "" })).toBe("d48a6d6");
  expect(releaseName({ sha, version: "v0.1.0" })).toBe("v0.1.0");
});

test("a verification reads as its outcome", () => {
  expect(verificationText(undefined)).toBe("");
  expect(verificationText({ passed: true, summary: "280 tests passed" })).toBe("280 tests passed");
  expect(verificationText({ passed: false, summary: "1 of 280 tests failed" })).toBe("1 of 280 tests failed");
  expect(verificationText({ passed: false, summary: "" })).toBe("failed");
  expect(verificationText({ passed: false, inconclusive: true, summary: "the stage was unreachable" }))
    .toBe("could not judge the release: the stage was unreachable");
  expect(verificationState({ passed: true, summary: "" })).toBe("succeeded");
  expect(verificationState({ passed: false, summary: "" })).toBe("failed");
  expect(verificationState({ passed: false, inconclusive: true, summary: "" })).toBe("inconclusive");
});

test("the notes say who wrote them", () => {
  expect(notesSourceText("agent")).toContain("release agent");
  expect(notesSourceText("fallback")).toContain("from the issues");
  expect(notesSourceText(undefined)).toBe("");
});

test("the version says what became of it", () => {
  expect(versionText({ status: "succeeded", version: "v0.2.0" })).toBe("v0.2.0");
  expect(versionText({ status: "succeeded", version: "v0.2.0", publish_error: "provider down" })).toBe("v0.2.0, not published: provider down");
  expect(versionText({ status: "succeeded", publish_error: "the tag already names another commit" }))
    .toBe("not versioned: the tag already names another commit");
  expect(versionText({ status: "succeeded" })).toBe("not versioned");
  expect(versionText({ status: "in_progress" })).toBe("versioned once every stage passes");
  expect(versionText({ status: "rolled_back" })).toBe("no version: its issues are in the next release");
  expect(isLive("pending") && isLive("in_progress")).toBe(true);
  expect(isLive("succeeded") || isLive("failed") || isLive("rolled_back")).toBe(false);
});
