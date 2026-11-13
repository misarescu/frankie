// Serves the web UI and proxies /api to the control plane, so the browser
// talks to one origin (no CORS) and server-sent events stream through.
import index from "./index.html";

export function createServer(controlplaneURL: string, port: number) {
  return Bun.serve({
    port,
    routes: { "/": index },
    async fetch(req) {
      const url = new URL(req.url);
      if (url.pathname === "/healthz") return new Response("ok\n");
      if (!url.pathname.startsWith("/api/")) return new Response("not found", { status: 404 });
      const upstream = await fetch(controlplaneURL + url.pathname + url.search, {
        method: req.method,
        headers: req.headers,
        body: req.method === "GET" || req.method === "HEAD" ? undefined : req.body,
        signal: req.signal,
      });
      // Pass the body through as a stream so SSE events arrive as they are sent.
      return new Response(upstream.body, { status: upstream.status, headers: upstream.headers });
    },
  });
}

if (import.meta.main) {
  const cp = process.env.CONTROLPLANE_URL ?? "http://localhost:8080";
  const server = createServer(cp, Number(process.env.PORT ?? 3000));
  console.log(JSON.stringify({ msg: "serving", url: server.url.href, controlplane: cp }));
}
