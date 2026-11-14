import { expect, test } from "bun:test";
import type { TokenStorage } from "./auth";
import { loadName, MAX_NAME, saveName, signed } from "./identity";

const mapStorage = (): TokenStorage & { data: Map<string, string> } => {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  };
};

test("M5.1: the name round-trips through storage, trimmed, and an empty one forgets it", () => {
  const s = mapStorage();
  expect(loadName(s)).toBeNull();
  saveName("  Ana  ", s);
  expect(loadName(s)).toBe("Ana");
  saveName("   ", s);
  expect(loadName(s)).toBeNull();
  expect(s.data.size).toBe(0);
});

test("M5.1: a name is cut to what the API accepts", () => {
  const s = mapStorage();
  saveName("a".repeat(MAX_NAME + 20), s);
  expect(loadName(s)).toHaveLength(MAX_NAME);
});

test("M5.1: a storage that throws keeps the name for this page", () => {
  const broken: TokenStorage = {
    getItem: () => { throw new Error("blocked"); },
    setItem: () => { throw new Error("blocked"); },
    removeItem: () => { throw new Error("blocked"); },
  };
  saveName("Bo", broken);
  expect(loadName(broken)).toBe("Bo");
  saveName("", broken);
  expect(loadName(broken)).toBeNull();
});

test("M5.1: signed adds by only when a name is set", () => {
  expect(signed({ text: "hi" }, "Ana")).toEqual({ text: "hi", by: "Ana" });
  expect(signed({ text: "hi" }, null)).toEqual({ text: "hi" });
  expect(signed({ text: "hi" }, "")).toEqual({ text: "hi" });
});
