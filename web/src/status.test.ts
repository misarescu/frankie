import { expect, test } from "bun:test";
import { isActive, statusLabel, statusTone } from "./status";

test("status tones and labels follow the mockup", () => {
  expect(statusTone("awaiting_human")).toBe("warn");
  expect(statusLabel("awaiting_human")).toBe("waiting for you");
  expect(statusTone("merged")).toBe("ok");
  expect(statusTone("failed")).toBe("bad");
  expect(statusTone("ready")).toBe("outline");
  expect(statusLabel("rolled_back")).toBe("rolled back");
  expect(statusLabel("not started")).toBe("not started");
  expect(statusTone("something_new")).toBe("neutral");
  expect(statusLabel("something_new_here")).toBe("something new here");
});

test("only statuses that move on by themselves pulse", () => {
  expect(isActive("running")).toBe(true);
  expect(isActive("refining")).toBe(true);
  expect(isActive("awaiting_human")).toBe(false);
  expect(isActive("merged")).toBe(false);
});
