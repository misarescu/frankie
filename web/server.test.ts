import { describe, expect, test } from "bun:test";
import { createServer } from "./server";

describe("web /api proxy (D147)", () => {
  test("strips spoofed forwarding headers when trustProxy is disabled", async () => {
    let seenProto: string | null = null;
    let seenHost: string | null = null;
    let seenXFF: string | null = null;
    let seenCF: string | null = null;
    const upstream = Bun.serve({
      port: 0,
      fetch(req) {
        seenProto = req.headers.get("x-forwarded-proto");
        seenHost = req.headers.get("x-forwarded-host");
        seenXFF = req.headers.get("x-forwarded-for");
        seenCF = req.headers.get("cf-connecting-ip");
        return new Response("ok");
      },
    });
    const web = createServer(upstream.url.origin, 0);
    try {
      const resp = await fetch(`${web.url.origin}/api/auth/login`, {
        headers: {
          "X-Forwarded-Proto": "https",
          "X-Forwarded-Host": "evil.example.com",
          "X-Forwarded-For": "203.0.113.99",
          "CF-Connecting-IP": "198.51.100.77",
        },
      });
      expect(resp.status).toBe(200);
      expect(seenProto).toBe("http");
      expect(seenHost).toBe(web.url.host);
      expect(seenXFF).not.toBe("203.0.113.99");
      expect(seenCF).toBeNull();
    } finally {
      web.stop(true);
      upstream.stop(true);
    }
  });

  test("strips CF-Connecting-IP and passes rightmost forwarded headers when trustProxy is enabled", async () => {
    let seenProto: string | null = null;
    let seenHost: string | null = null;
    let seenXFF: string | null = null;
    let seenCF: string | null = null;
    let seenRealIP: string | null = null;
    const upstream = Bun.serve({
      port: 0,
      fetch(req) {
        const u = new URL(req.url);
        seenProto = req.headers.get("x-forwarded-proto");
        seenHost = req.headers.get("x-forwarded-host");
        seenXFF = req.headers.get("x-forwarded-for");
        seenCF = req.headers.get("cf-connecting-ip");
        seenRealIP = req.headers.get("x-real-ip");
        if (u.pathname === "/api/auth/sso/google/start") {
          const headers = new Headers({ Location: "https://accounts.google.com/o/oauth2/v2/auth?state=abc" });
          headers.append("Set-Cookie", "frankie_oauth_state=signed; Path=/api/auth/sso; HttpOnly; SameSite=Lax");
          return new Response(null, { status: 302, headers });
        }
        return new Response("ok");
      },
    });
    const web = createServer(upstream.url.origin, 0, { trustProxy: true });
    try {
      const resp = await fetch(`${web.url.origin}/api/auth/sso/google/start`, {
        redirect: "manual",
        headers: {
          "X-Forwarded-Proto": "http, https",
          "X-Forwarded-Host": "evil.example.com, frankie.example.com",
          "X-Forwarded-For": "198.51.100.1, 203.0.113.42",
          "CF-Connecting-IP": "198.51.100.77",
          "X-Real-IP": "198.51.100.88",
        },
      });
      expect(resp.status).toBe(302);
      expect(resp.headers.get("location")).toBe("https://accounts.google.com/o/oauth2/v2/auth?state=abc");
      expect(resp.headers.get("set-cookie")).toContain("frankie_oauth_state=signed");
      expect(seenProto).toBe("https");
      expect(seenHost).toBe("frankie.example.com");
      expect(seenXFF).toBe("203.0.113.42");
      expect(seenCF).toBeNull();
      expect(seenRealIP).toBeNull();
    } finally {
      web.stop(true);
      upstream.stop(true);
    }
  });
});
