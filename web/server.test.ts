import { describe, expect, test } from "bun:test";
import { createServer } from "./server";

describe("web /api proxy (D147)", () => {
  test("passes 302 redirects, Set-Cookie headers, and forwarded headers through untouched", async () => {
    let seenProto: string | null = null;
    let seenHost: string | null = null;
    const upstream = Bun.serve({
      port: 0,
      fetch(req) {
        const u = new URL(req.url);
        seenProto = req.headers.get("x-forwarded-proto");
        seenHost = req.headers.get("x-forwarded-host");
        if (u.pathname === "/api/auth/sso/google/start") {
          const headers = new Headers({ Location: "https://accounts.google.com/o/oauth2/v2/auth?state=abc" });
          headers.append("Set-Cookie", "frankie_oauth_state=signed; Path=/api/auth/sso; HttpOnly; SameSite=Lax");
          return new Response(null, { status: 302, headers });
        }
        return new Response("ok");
      },
    });
    const web = createServer(upstream.url.origin, 0);
    try {
      const resp = await fetch(`${web.url.origin}/api/auth/sso/google/start`, {
        redirect: "manual",
        headers: { "X-Forwarded-Proto": "https", "X-Forwarded-Host": "frankie.example.com" },
      });
      expect(resp.status).toBe(302);
      expect(resp.headers.get("location")).toBe("https://accounts.google.com/o/oauth2/v2/auth?state=abc");
      expect(resp.headers.get("set-cookie")).toContain("frankie_oauth_state=signed");
      expect(seenProto).toBe("https");
      expect(seenHost).toBe("frankie.example.com");
    } finally {
      web.stop(true);
      upstream.stop(true);
    }
  });
});
