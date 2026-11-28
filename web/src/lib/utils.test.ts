import { expect, test } from "bun:test";
import { cn } from "./utils";

test("the theme's text-xs2 size survives a text colour after it", () => {
  expect(cn("text-xs2 font-medium", "text-muted-fg")).toBe("text-xs2 font-medium text-muted-fg");
});

test("a later size still wins over text-xs2", () => {
  expect(cn("text-xs2", "text-sm")).toBe("text-sm");
});

test("a later colour still wins over an earlier one", () => {
  expect(cn("text-muted-fg", "text-bad")).toBe("text-bad");
});
