import { expect, test } from "bun:test";
import { cn } from "./utils";

test("keeps the theme's text-xs2 size beside a text color", () => {
  expect(cn("text-xs2 font-medium", "text-ok")).toBe("text-xs2 font-medium text-ok");
  expect(cn("text-xs2", "text-xs")).toBe("text-xs");
  expect(cn("text-muted-fg", "text-bad")).toBe("text-bad");
});
