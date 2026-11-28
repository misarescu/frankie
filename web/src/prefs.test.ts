import { expect, test } from "bun:test";
import { loadLastProject, loadTheme, resolveTheme, saveLastProject, saveTheme, startProject } from "./prefs";

const memory = () => {
  const m = new Map<string, string>();
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k), m };
};

test("the theme follows the system until a person picks one", () => {
  const s = memory();
  expect(loadTheme(s)).toBeNull();
  expect(resolveTheme(loadTheme(s), true)).toBe("dark");
  saveTheme("light", s);
  expect(s.m.get("frankie.theme")).toBe("light");
  expect(resolveTheme(loadTheme(s), true)).toBe("light");
  saveTheme(null, s);
  expect(loadTheme(s)).toBeNull();
  s.setItem("frankie.theme", "purple");
  expect(loadTheme(s)).toBeNull();
});

test("the last project opens first while it exists", () => {
  const s = memory();
  expect(loadLastProject(s)).toBeNull();
  saveLastProject("frk", s);
  expect(s.m.get("frankie.project")).toBe("frk");
  expect(startProject(["gc", "frk"], loadLastProject(s))).toBe("frk");
  expect(startProject(["gc"], loadLastProject(s))).toBe("gc");
  expect(startProject([], null)).toBeUndefined();
});

test("storage that throws does not break the page", () => {
  const broken = { getItem: () => { throw new Error("blocked"); }, setItem: () => { throw new Error("blocked"); }, removeItem: () => { throw new Error("blocked"); } };
  expect(loadTheme(broken)).toBeNull();
  expect(() => saveTheme("dark", broken)).not.toThrow();
  expect(loadLastProject(broken)).toBeNull();
});
