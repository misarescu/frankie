import { expect, test } from "bun:test";
import { clearToken, loadToken, saveToken, withToken, type TokenStorage } from "./auth";

const mapStorage = (): TokenStorage & { data: Map<string, string> } => {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  };
};

test("the token round-trips through storage, trimmed, and clears", () => {
  const s = mapStorage();
  expect(loadToken(s)).toBeNull();
  saveToken("  abc123  ", s);
  expect(loadToken(s)).toBe("abc123");
  clearToken(s);
  expect(loadToken(s)).toBeNull();
  expect(s.data.size).toBe(0);
});

test("a storage that throws keeps the token for this page", () => {
  const broken: TokenStorage = {
    getItem: () => { throw new Error("blocked"); },
    setItem: () => { throw new Error("blocked"); },
    removeItem: () => { throw new Error("blocked"); },
  };
  saveToken("xyz", broken);
  expect(loadToken(broken)).toBe("xyz");
  clearToken(broken);
  expect(loadToken(broken)).toBeNull();
});

test("withToken sends the token as a bearer header and keeps other headers", async () => {
  let seen: Headers | undefined;
  const f = withToken(async (_url, init) => {
    seen = new Headers(init?.headers);
    return new Response(null, { status: 202 });
  }, () => "tok");
  const r = await f("/api/x", { method: "POST", headers: { "Content-Type": "application/json" } });
  expect(r.status).toBe(202);
  expect(seen?.get("Authorization")).toBe("Bearer tok");
  expect(seen?.get("Content-Type")).toBe("application/json");
});

test("withToken sends no header without a token", async () => {
  let seen: Headers | undefined;
  const f = withToken(async (_url, init) => {
    seen = new Headers(init?.headers);
    return new Response(null, { status: 200 });
  }, () => null);
  await f("/api/x");
  expect(seen?.has("Authorization")).toBe(false);
});

test("a 401 notifies the page and says what to do", async () => {
  let rejected = 0;
  const unauthorized = async () => new Response("missing or wrong API token\n", { status: 401 });
  await expect(withToken(unauthorized, () => null, () => rejected++)("/api/x")).rejects.toThrow("needs the API token");
  await expect(withToken(unauthorized, () => "bad", () => rejected++)("/api/x")).rejects.toThrow("was rejected");
  expect(rejected).toBe(2);
});
