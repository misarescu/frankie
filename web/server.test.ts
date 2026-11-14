import { afterAll, expect, test } from "bun:test";
import { createServer } from "./server";

// A stand-in control plane that streams two SSE events with a pause between.
const upstream = Bun.serve({
  port: 0,
  fetch(req) {
    const url = new URL(req.url);
    if (url.pathname === "/api/runs") return Response.json([{ ID: "r1" }]);
    if (url.pathname === "/api/echo-auth") return new Response(req.headers.get("authorization") ?? "", { status: 202 });
    if (url.pathname === "/api/runs/r1/stream") {
      const stream = new ReadableStream({
        async start(c) {
          c.enqueue(new TextEncoder().encode("id: 1\nevent: tool_call\ndata: {}\n\n"));
          // Quiet for longer than the web server's idle timeout below.
          await Bun.sleep(6000);
          c.enqueue(new TextEncoder().encode("event: end\ndata: {}\n\n"));
          c.close();
        },
      });
      return new Response(stream, { headers: { "content-type": "text/event-stream" } });
    }
    return new Response("nope", { status: 404 });
  },
});
// A 1s idle timeout stands in for Bun's 10s default, so the test stays fast.
const web = createServer(upstream.url.href.replace(/\/$/, ""), 0, { idleTimeout: 1 });
afterAll(() => { web.stop(true); upstream.stop(true); });

test("proxies the API", async () => {
  const r = await fetch(new URL("/api/runs", web.url));
  expect(r.status).toBe(200);
  expect(await r.json()).toEqual([{ ID: "r1" }]);
});

// D58: write requests carry the API token, so the proxy must pass it on.
test("passes the Authorization header and the body of a POST", async () => {
  const r = await fetch(new URL("/api/echo-auth", web.url), { method: "POST", headers: { Authorization: "Bearer tok" }, body: "{}" });
  expect(r.status).toBe(202);
  expect(await r.text()).toBe("Bearer tok");
});

test("streams SSE through without buffering, and outlives the idle timeout", async () => {
  const r = await fetch(new URL("/api/runs/r1/stream", web.url));
  expect(r.headers.get("content-type")).toBe("text/event-stream");
  const reader = r.body!.getReader();
  const first = new TextDecoder().decode((await reader.read()).value);
  expect(first).toContain("event: tool_call"); // arrives before the upstream finished
  let rest = "";
  for (let c = await reader.read(); !c.done; c = await reader.read()) rest += new TextDecoder().decode(c.value);
  expect(rest).toContain("event: end");
}, 10_000);

test("serves health and the app, 404 elsewhere", async () => {
  expect((await fetch(new URL("/healthz", web.url))).status).toBe(200);
  const page = await fetch(new URL("/", web.url));
  expect(page.status).toBe(200);
  expect(await page.text()).toContain('<div id="root">');
  expect((await fetch(new URL("/nope", web.url))).status).toBe(404);
});
