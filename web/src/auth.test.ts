import { describe, expect, test } from "bun:test";
import { canOperate, displayUserName, isAdmin, roleLabel, safeNextPath, ssoErrorMessage, withSession } from "./auth";

describe("auth helpers (M10)", () => {
  test("role helpers match D146 hierarchy", () => {
    expect(canOperate("admin")).toBe(true);
    expect(canOperate("member")).toBe(true);
    expect(canOperate("viewer")).toBe(false);
    expect(canOperate(undefined)).toBe(false);

    expect(isAdmin("admin")).toBe(true);
    expect(isAdmin("member")).toBe(false);
    expect(isAdmin("viewer")).toBe(false);

    expect(roleLabel("admin")).toBe("Admin");
    expect(roleLabel("member")).toBe("Member");
    expect(roleLabel("viewer")).toBe("Viewer");
  });

  test("displayUserName prefers display_name over username", () => {
    expect(displayUserName({ display_name: "Mihai", username: "mihai" })).toBe("Mihai");
    expect(displayUserName({ display_name: "  ", username: "mihai" })).toBe("mihai");
  });

  test("safeNextPath rejects open redirects and auth loops", () => {
    expect(safeNextPath(undefined)).toBe("/");
    expect(safeNextPath("/projects/frk/board?view=graph")).toBe("/projects/frk/board?view=graph");
    expect(safeNextPath("https://evil.example")).toBe("/");
    expect(safeNextPath("//evil.example")).toBe("/");
    expect(safeNextPath("/login")).toBe("/");
    expect(safeNextPath("/login?next=/")).toBe("/");
    expect(safeNextPath("/invite/abc")).toBe("/");
  });

  test("ssoErrorMessage maps known codes", () => {
    expect(ssoErrorMessage(undefined)).toBeNull();
    expect(ssoErrorMessage("not_allowed")).toContain("allowlist");
    expect(ssoErrorMessage("unknown_code")).toContain("Sign-in failed");
  });

  test("withSession sends same-origin credentials and notifies on 401", async () => {
    let seenCreds: RequestCredentials | undefined;
    let unauthCount = 0;
    const f = withSession(async (_url, init) => {
      seenCreds = init?.credentials;
      return new Response("unauthorized", { status: 401 });
    }, () => { unauthCount++; });

    await expect(f("/api/issues/start", { method: "POST" })).rejects.toThrow(/session has expired/i);
    expect(seenCreds).toBe("same-origin");
    expect(unauthCount).toBe(1);
  });
});
