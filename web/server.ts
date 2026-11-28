// Serves the web UI and proxies /api to the control plane, so the browser
// talks to one origin (no CORS), cookies and OAuth redirects pass through
// untouched (D147), and server-sent events stream through. Every other path
// is the app (M9, D141): the router reads the URL in the browser.
import index from "./index.html";

// Bun closes connections idle for idleTimeout seconds (default 10). Event
// streams are quiet for minutes while a toolchain builds, so they are exempt.
export function createServer(controlplaneURL: string, port: number, opts: { idleTimeout?: number } = {}) {
  return Bun.serve({
    port,
    idleTimeout: opts.idleTimeout,
    // Bundled once and minified in production. In development Bun bundles on
    // each change, without hot module replacement: its module format breaks
    // TanStack Router's circular imports (router-core's router.js and
    // load-client.js), and the app renders nothing.
    development: process.env.NODE_ENV === "production" ? false : { hmr: false },
    routes: {
      "/healthz": () => new Response("ok\n"),
      "/api/*": async (req, server) => {
        const url = new URL(req.url);
        const headers = new Headers(req.headers);
        if (!headers.has("x-forwarded-proto")) {
          headers.set("x-forwarded-proto", url.protocol.replace(/:$/, ""));
        }
        if (!headers.has("x-forwarded-host")) {
          headers.set("x-forwarded-host", req.headers.get("host") || url.host);
        }
        const ip = server.requestIP(req)?.address;
        if (ip && !headers.has("x-forwarded-for")) {
          headers.set("x-forwarded-for", ip);
        }
        let upstream: Response;
        try {
          upstream = await fetch(controlplaneURL + url.pathname + url.search, {
            method: req.method,
            headers,
            body: req.method === "GET" || req.method === "HEAD" ? undefined : req.body,
            redirect: "manual",
            signal: req.signal,
          });
        } catch (e) {
          // The UI tells the control plane being down from an API error by this.
          return new Response(`the control plane is not reachable: ${(e as Error).message}\n`, { status: 502 });
        }
        if (upstream.headers.get("content-type")?.startsWith("text/event-stream")) server.timeout(req, 0);
        // Pass the body and headers (including Set-Cookie and Location) straight through.
        return new Response(upstream.body, { status: upstream.status, headers: upstream.headers });
      },
      "/*": index,
    },
  });
}

if (import.meta.main) {
  const cp = process.env.CONTROLPLANE_URL ?? "http://localhost:8080";
  const server = createServer(cp, Number(process.env.PORT ?? 3000));
  console.log(JSON.stringify({ msg: "serving", url: server.url.href, controlplane: cp }));
}
