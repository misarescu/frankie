#!/usr/bin/env node
// walk.mjs: load every page of the web UI in a fresh headless Chromium, in each
// viewport and theme, and report what went wrong: console errors, uncaught page
// errors and /api responses with status >= 400 that the paths file does not
// expect. Takes a full-page screenshot of each. Page-agnostic: it knows nothing
// about Frankie's pages beyond the frankie.theme localStorage key.
//
//   node walk.mjs <base-url> <paths.json> [--out <dir>] [--viewports desktop,phone] [--themes light,dark]
//   node walk.mjs --history <base-url> <steps.json> [--out <dir>] [--viewport desktop] [--theme light]
//
// paths.json is an array of paths, or an object:
//   { "storage": {"frankie.apiToken": "..."},          localStorage set before every load
//     "expect":  [{"url": "/api/gates/", "status": 503}], /api failures allowed on every page
//     "ignoreConsole": ["regex"],                        console errors to ignore
//     "paths": ["/projects/vfy/board",
//               {"path": "/issues/nope", "name": "issue-missing", "expect": [{"url": "/api/issues/nope", "status": 404}]}] }
// An expect entry's url is a substring of the request URL, or /regex/; status
// is a number, a list of numbers, or "any". Status 0 is a request that failed
// without a response (connection refused, reset).
//
// steps.json for --history is { "storage": {...}, "expect": [...], "steps": [step, ...] }, a
// step being {"goto": "<path>"} or {"click": <locator>} (locator: {"role", "name"} |
// {"text"} | {"label"} | {"placeholder"} | {"css"}, with "exact" and "nth"); a
// step that should not change the URL says "sameUrl": true. It runs the steps,
// recording each URL and the page's first h1, then walks back to the first
// (reloading at each), then forward to the last, and checks every URL and h1
// comes back.
//
// Writes <out>/report.json (or history.json) and the screenshots; exit 0 when
// nothing unexpected happened, 1 otherwise. --out defaults to
// $FRANKIE_VERIFY_EVIDENCE/walk-<time> (~/.frankie-verify/evidence/...).
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { homedir } from "node:os";
import { join } from "node:path";

const PW_DIR = process.env.FRANKIE_VERIFY_PLAYWRIGHT ?? join(homedir(), ".cache", "frankie-verify", "playwright");
const EVIDENCE_ROOT = process.env.FRANKIE_VERIFY_EVIDENCE ?? join(homedir(), ".frankie-verify", "evidence");
const VIEWPORTS = { desktop: { width: 1440, height: 900 }, phone: { width: 390, height: 844 } };
const LOAD_TIMEOUT = Number(process.env.FRANKIE_VERIFY_TIMEOUT_MS ?? 15000);
const IDLE_MS = 500;

function die(msg) {
  console.error("walk: " + msg);
  process.exit(2);
}

function parse(argv) {
  const flags = {}, pos = [];
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--history") flags.history = true;
    else if (a.startsWith("--")) flags[a.slice(2)] = argv[++i] ?? die(a + " needs a value");
    else pos.push(a);
  }
  return { flags, pos };
}

const matches = (pattern, url) =>
  pattern.length > 2 && pattern.startsWith("/") && pattern.endsWith("/") ? new RegExp(pattern.slice(1, -1)).test(url) : url.includes(pattern);

function expected(list, url, status) {
  return list.some((e) => matches(e.url, url) && (e.status === "any" || (Array.isArray(e.status) ? e.status.includes(status) : e.status === status)));
}

// watch records what goes wrong on a page and tracks its requests in flight,
// leaving out event streams, which stay open as long as the page does.
function watch(page, expect, ignoreConsole) {
  const seen = { consoleErrors: [], pageErrors: [], apiErrors: [], expectedApiErrors: [] };
  const inflight = new Set();
  let lastChange = Date.now();
  const streaming = (req) => req.resourceType() === "eventsource" || /\/stream(\?|$)/.test(req.url());
  page.on("request", (req) => {
    if (!streaming(req)) { inflight.add(req); lastChange = Date.now(); }
  });
  const done = (req) => { if (inflight.delete(req)) lastChange = Date.now(); };
  page.on("requestfinished", done);
  page.on("requestfailed", (req) => {
    done(req);
    const err = req.failure()?.errorText ?? "";
    // Aborted requests are the page's own doing (navigation, a closed stream).
    if (!new URL(req.url()).pathname.startsWith("/api/") || err.includes("ERR_ABORTED")) return;
    const rec = { method: req.method(), url: req.url(), status: 0, error: err };
    (expected(expect, req.url(), 0) ? seen.expectedApiErrors : seen.apiErrors).push(rec);
  });
  page.on("response", (res) => {
    if (res.status() < 400 || !new URL(res.url()).pathname.startsWith("/api/")) return;
    const rec = { method: res.request().method(), url: res.url(), status: res.status() };
    (expected(expect, res.url(), res.status()) ? seen.expectedApiErrors : seen.apiErrors).push(rec);
  });
  page.on("pageerror", (e) => seen.pageErrors.push(String(e.stack ?? e.message ?? e)));
  page.on("console", (msg) => {
    if (msg.type() !== "error") return;
    const text = msg.text();
    if (ignoreConsole.some((re) => new RegExp(re).test(text))) return;
    // Chromium logs every failed resource load; an expected /api status is not an error.
    const m = /status of (\d+)/.exec(text);
    const loc = msg.location()?.url ?? "";
    if (/^Failed to load resource/.test(text) && expected(expect, loc, m ? Number(m[1]) : 0)) return;
    seen.consoleErrors.push(loc ? text + " (" + loc + ")" : text);
  });
  // idle resolves once no request has been in flight for IDLE_MS, or false at the timeout.
  // The clock starts at the call, so requests an action starts a moment later still count.
  const idle = async () => {
    lastChange = Date.now();
    const until = lastChange + LOAD_TIMEOUT;
    while (Date.now() < until) {
      if (inflight.size === 0 && Date.now() - lastChange >= IDLE_MS) return true;
      await new Promise((r) => setTimeout(r, 100));
    }
    return false;
  };
  return { seen, idle };
}

async function context(browser, viewport, theme, storage) {
  const ctx = await browser.newContext({ viewport: VIEWPORTS[viewport] ?? die("unknown viewport " + viewport), colorScheme: theme === "dark" ? "dark" : "light" });
  await ctx.addInitScript(({ theme, storage }) => {
    try {
      localStorage.setItem("frankie.theme", theme);
      for (const [k, v] of Object.entries(storage)) localStorage.setItem(k, v);
    } catch {
      // about:blank has no storage
    }
  }, { theme, storage });
  return ctx;
}

const slug = (s) => s.replace(/^\/+/, "").replace(/[^a-zA-Z0-9]+/g, "-").replace(/^-|-$/g, "") || "root";
// brief is the first line of each distinct problem, for the console; report.json has them whole.
const first = (s) => String(s).split("\n")[0];
const brief = (list) => [...new Set(list.filter(Boolean).map(first))].join(" | ");
const ok = (s) => s.consoleErrors.length === 0 && s.pageErrors.length === 0 && s.apiErrors.length === 0;
const heading = (page) => page.locator("h1").first().innerText({ timeout: 1000 }).catch(() => null);

async function walkPaths(browser, base, spec, out, flags) {
  const viewports = (flags.viewports ?? "desktop,phone").split(",");
  const themes = (flags.themes ?? "light,dark").split(",");
  const results = [];
  for (const entry of spec.paths) {
    const p = typeof entry === "string" ? { path: entry } : entry;
    const name = p.name ?? slug(p.path);
    for (const viewport of viewports) {
      for (const theme of themes) {
        const ctx = await context(browser, viewport, theme, { ...(spec.storage ?? {}), ...(p.storage ?? {}) });
        const page = await ctx.newPage();
        const { seen, idle } = watch(page, [...(spec.expect ?? []), ...(p.expect ?? [])], spec.ignoreConsole ?? []);
        let loadError = null, settled = false;
        try {
          await page.goto(new URL(p.path, base).href, { waitUntil: "load", timeout: LOAD_TIMEOUT });
          settled = await idle();
        } catch (e) {
          loadError = String(e.message).split("\n")[0];
        }
        const shot = join(out, name + "." + viewport + "." + theme + ".png");
        await page.screenshot({ path: shot, fullPage: true }).catch(() => {});
        const r = { name, path: p.path, viewport, theme, url: page.url(), h1: await heading(page), screenshot: shot, settled, loadError, ...seen };
        r.ok = ok(seen) && !loadError;
        results.push(r);
        console.log((r.ok ? "ok    " : "FAIL  ") + name + " " + viewport + " " + theme + (settled ? "" : " (network never idle)") +
          (r.ok ? "" : ": " + brief([loadError, ...seen.pageErrors, ...seen.consoleErrors, ...seen.apiErrors.map((a) => a.method + " " + a.url + " " + a.status)])));
        await ctx.close();
      }
    }
  }
  const report = { base, at: new Date().toISOString(), ok: results.every((r) => r.ok), results };
  writeFileSync(join(out, "report.json"), JSON.stringify(report, null, 2));
  return report.ok;
}

function locate(page, l) {
  let loc;
  if (l.role) loc = page.getByRole(l.role, { ...(l.name ? { name: l.name } : {}), exact: l.exact === true });
  else if (l.text) loc = page.getByText(l.text, { exact: l.exact === true });
  else if (l.label) loc = page.getByLabel(l.label, { exact: l.exact === true });
  else if (l.placeholder) loc = page.getByPlaceholder(l.placeholder, { exact: l.exact === true });
  else if (l.css) loc = page.locator(l.css);
  else die("a click needs role, text, label, placeholder or css: " + JSON.stringify(l));
  return l.nth !== undefined ? loc.nth(l.nth) : loc.first();
}

async function walkHistory(browser, base, spec, out, flags) {
  const viewport = flags.viewport ?? "desktop", theme = flags.theme ?? "light";
  const ctx = await context(browser, viewport, theme, spec.storage ?? {});
  const page = await ctx.newPage();
  const { seen, idle } = watch(page, spec.expect ?? [], spec.ignoreConsole ?? []);
  const problems = [], visited = [], checks = [];
  const here = async () => ({ url: page.url(), h1: await heading(page) });
  for (const [i, step] of spec.steps.entries()) {
    const before = page.url();
    try {
      if (step.goto) await page.goto(new URL(step.goto, base).href, { waitUntil: "load", timeout: LOAD_TIMEOUT });
      else if (step.click) await locate(page, step.click).click({ timeout: LOAD_TIMEOUT });
      else die("a step needs goto or click: " + JSON.stringify(step));
    } catch (e) {
      problems.push("step " + i + " " + JSON.stringify(step) + ": " + String(e.message).split("\n")[0]);
      break;
    }
    await idle();
    const at = await here();
    if (i > 0 && at.url === before && !step.sameUrl) problems.push("step " + i + " " + JSON.stringify(step) + " did not change the URL (" + at.url + ")");
    const shot = join(out, "step-" + String(i).padStart(2, "0") + ".png");
    await page.screenshot({ path: shot, fullPage: true }).catch(() => {});
    visited.push({ step, ...at, screenshot: shot });
    console.log("step  " + i + " " + at.url + (at.h1 ? "  [" + at.h1 + "]" : ""));
  }
  const expectAt = async (how, want) => {
    await idle();
    const got = await here();
    const pass = got.url === want.url && got.h1 === want.h1;
    checks.push({ how, want: { url: want.url, h1: want.h1 }, got, pass });
    console.log((pass ? "ok    " : "FAIL  ") + how + " " + got.url + (pass ? "" : " want " + want.url + (got.h1 !== want.h1 ? " (h1 " + JSON.stringify(got.h1) + ", want " + JSON.stringify(want.h1) + ")" : "")));
  };
  if (problems.length === 0) {
    for (let i = visited.length - 1; i > 0; i--) {
      await page.goBack({ timeout: LOAD_TIMEOUT }).catch((e) => problems.push("back to step " + (i - 1) + ": " + String(e.message).split("\n")[0]));
      await expectAt("back", visited[i - 1]);
      await page.reload({ timeout: LOAD_TIMEOUT }).catch((e) => problems.push("reload at step " + (i - 1) + ": " + String(e.message).split("\n")[0]));
      await expectAt("reload", visited[i - 1]);
    }
    for (let i = 1; i < visited.length; i++) {
      await page.goForward({ timeout: LOAD_TIMEOUT }).catch((e) => problems.push("forward to step " + i + ": " + String(e.message).split("\n")[0]));
      await expectAt("forward", visited[i]);
    }
  }
  await ctx.close();
  const report = { base, viewport, theme, at: new Date().toISOString(), visited, checks, problems, ...seen };
  report.ok = problems.length === 0 && checks.every((c) => c.pass) && ok(seen);
  for (const p of [...new Set([...problems, ...seen.pageErrors, ...seen.consoleErrors, ...seen.apiErrors.map((a) => a.method + " " + a.url + " " + a.status)].map(first))]) console.log("FAIL  " + p);
  writeFileSync(join(out, "history.json"), JSON.stringify(report, null, 2));
  return report.ok;
}

async function main() {
  const { flags, pos } = parse(process.argv.slice(2));
  const [base, file] = pos;
  if (!base || !file) die("usage: walk.mjs [--history] <base-url> <paths.json|steps.json> [--out <dir>] (see the file header)");
  const raw = JSON.parse(readFileSync(file, "utf8"));
  const spec = Array.isArray(raw) ? { paths: raw } : raw;
  const out = flags.out ?? join(EVIDENCE_ROOT, "walk-" + new Date().toISOString().replace(/[:.]/g, "-"));
  mkdirSync(out, { recursive: true });
  let chromium;
  try {
    ({ chromium } = createRequire(join(PW_DIR, "noop.js"))("playwright"));
  } catch {
    die("Playwright is not installed in " + PW_DIR + "; run frankie-verify launch (it installs it)");
  }
  const browser = await chromium.launch({ headless: true });
  let passed;
  try {
    passed = flags.history ? await walkHistory(browser, base, spec, out, flags) : await walkPaths(browser, base, spec, out, flags);
  } finally {
    await browser.close();
  }
  console.log((passed ? "PASS" : "FAIL") + "  report in " + out);
  process.exit(passed ? 0 : 1);
}

main();
