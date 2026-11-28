#!/usr/bin/env node
// browser.mjs: drive the verification instance's web UI in a persistent headless
// Chromium (Playwright over CDP). One browser per instance; every command is a
// separate short process that attaches, acts and detaches, so page state (the
// hash route, localStorage with the API token and name) carries between commands.
//
//   node browser.mjs start | status | stop
//   node browser.mjs goto </path | #/route | url>
//   node browser.mjs click  <locator> [--accept-dialog]
//   node browser.mjs fill   <locator> --value <text>
//   node browser.mjs check  <locator>                      tick a checkbox
//   node browser.mjs expect <locator> [--absent] [--value <text>] [--enabled|--disabled] [--checked]
//   node browser.mjs text   [<locator>]                    print visible text (body by default)
//   node browser.mjs snapshot   --path <file>              ARIA snapshot of the page
//   node browser.mjs screenshot --path <file> [--full]
//
// Locator: --role <r> --name <n> | --text <t> | --label <l> | --placeholder <p>,
// narrowed with --in-row <text> (a table row), --in-section <text> (a card),
// --exact, --nth <i>. Each action is appended to <evidence>/actions.log, tagged
// with $FRANKIE_VERIFY_FEATURE when set. Exit 0 on success, 1 on a failed
// expectation or action.
import { spawn } from "node:child_process";
import { appendFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createRequire } from "node:module";
import { homedir, tmpdir } from "node:os";
import { dirname, join } from "node:path";

const STATE_ROOT = process.env.FRANKIE_VERIFY_STATE ?? join(process.env.TMPDIR ?? tmpdir(), "frankie-verify");
const EVIDENCE_ROOT = process.env.FRANKIE_VERIFY_EVIDENCE ?? join(homedir(), ".frankie-verify", "evidence");
const PW_DIR = process.env.FRANKIE_VERIFY_PLAYWRIGHT ?? join(homedir(), ".cache", "frankie-verify", "playwright");
const TIMEOUT = Number(process.env.FRANKIE_VERIFY_TIMEOUT_MS ?? 10000);

function loadState() {
  const runId = process.env.FRANKIE_VERIFY_RUN ?? (existsSync(join(STATE_ROOT, "current")) ? readFileSync(join(STATE_ROOT, "current"), "utf8").trim() : "");
  if (!runId) fail("no instance: run frankie-verify launch first");
  const runDir = join(STATE_ROOT, "runs", runId);
  if (!existsSync(join(runDir, "state.env"))) fail(`run ${runId} has no state at ${runDir}`);
  const env = {};
  for (const line of readFileSync(join(runDir, "state.env"), "utf8").split("\n")) {
    const m = /^([A-Z_]+)=(.*)$/.exec(line);
    if (m) env[m[1]] = m[2].replace(/\\(.)/g, "$1");
  }
  return { runId, runDir, env, evidence: join(EVIDENCE_ROOT, runId) };
}

function fail(msg) {
  console.error(`browser: ${msg}`);
  process.exit(1);
}

function parse(argv) {
  const flags = {}, pos = [];
  const bool = new Set(["absent", "enabled", "disabled", "checked", "exact", "full", "accept-dialog"]);
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (!a.startsWith("--")) { pos.push(a); continue; }
    const k = a.slice(2);
    if (bool.has(k)) flags[k] = true;
    else flags[k] = argv[++i] ?? fail(`--${k} needs a value`);
  }
  return { flags, pos };
}

async function playwright() {
  const req = createRequire(join(PW_DIR, "noop.js"));
  try {
    return req("playwright");
  } catch {
    fail(`Playwright is not installed in ${PW_DIR}; run frankie-verify launch (it installs it)`);
  }
}

async function cdpUp(port) {
  try {
    return (await fetch(`http://127.0.0.1:${port}/json/version`)).ok;
  } catch {
    return false;
  }
}

async function start(st) {
  const port = Number(st.env.CDP_PORT);
  if (await cdpUp(port)) return console.log(`browser already up on ${port}`);
  const { chromium } = await playwright();
  const profile = join(st.runDir, "chrome-profile");
  mkdirSync(profile, { recursive: true });
  const child = spawn(chromium.executablePath(), [
    `--remote-debugging-port=${port}`, "--remote-debugging-address=127.0.0.1", `--user-data-dir=${profile}`,
    "--headless=new", "--no-first-run", "--no-default-browser-check", "--window-size=1280,900", "about:blank",
  ], { detached: true, stdio: "ignore" });
  child.unref();
  writeFileSync(join(st.runDir, "browser.pid"), String(child.pid));
  for (let i = 0; i < 50; i++) {
    if (await cdpUp(port)) return console.log(`browser up on ${port}`);
    await new Promise((r) => setTimeout(r, 200));
  }
  fail("Chromium did not open its debugging port");
}

function stop(st) {
  const f = join(st.runDir, "browser.pid");
  if (!existsSync(f)) return console.log("no browser to stop");
  const pid = Number(readFileSync(f, "utf8"));
  try { process.kill(pid); } catch { /* already gone */ }
  console.log(`stopped browser ${pid}`);
}

function locator(page, f) {
  let scope = page;
  if (f["in-row"]) scope = page.locator("tr", { hasText: f["in-row"] });
  else if (f["in-section"]) scope = page.locator("section", { hasText: f["in-section"] });
  const exact = f.exact === true;
  let loc;
  if (f.role) loc = scope.getByRole(f.role, { ...(f.name ? { name: f.name } : {}), exact });
  else if (f.text) loc = scope.getByText(f.text, { exact });
  else if (f.label) loc = scope.getByLabel(f.label, { exact });
  else if (f.placeholder) loc = scope.getByPlaceholder(f.placeholder, { exact });
  else fail("a locator is needed: --role/--name, --text, --label or --placeholder");
  return f.nth !== undefined ? loc.nth(Number(f.nth)) : loc.first();
}

const describe = (f) => ["role", "name", "text", "label", "placeholder", "in-row", "in-section", "nth"].filter((k) => f[k] !== undefined).map((k) => `${k}=${JSON.stringify(f[k])}`).join(" ");

async function main() {
  const [cmd, ...rest] = process.argv.slice(2);
  if (!cmd) fail("usage: browser.mjs start|status|stop|goto|click|fill|check|expect|text|snapshot|screenshot (see the file header)");
  const { flags, pos } = parse(rest);
  const st = loadState();
  const port = Number(st.env.CDP_PORT);
  if (cmd === "start") return start(st);
  if (cmd === "stop") return stop(st);
  if (cmd === "status") return (await cdpUp(port)) ? console.log(`up on ${port}`) : fail("browser is not running");

  const { chromium } = await playwright();
  const browser = await chromium.connectOverCDP(`http://127.0.0.1:${port}`);
  const ctx = browser.contexts()[0];
  const page = ctx.pages()[0] ?? (await ctx.newPage());
  page.setDefaultTimeout(TIMEOUT);
  mkdirSync(st.evidence, { recursive: true });
  const log = (line) => appendFileSync(join(st.evidence, "actions.log"), `${new Date().toISOString()} [${process.env.FRANKIE_VERIFY_FEATURE ?? "-"}] ${line}\n`);
  let ok = true;
  try {
    switch (cmd) {
      case "goto": {
        const to = pos[0] ?? fail("goto needs a /path, a #/route or a URL");
        const base = `http://127.0.0.1:${st.env.WEB_PORT}/`;
        const url = to.startsWith("#") ? base + to : to.startsWith("/") ? base + to.slice(1) : to;
        await page.goto(url);
        if (to.startsWith("#")) await page.reload();
        log(`goto ${to}`);
        console.log(`at ${page.url()}`);
        break;
      }
      case "click": {
        const loc = locator(page, flags);
        if (flags["accept-dialog"]) page.once("dialog", (d) => d.accept());
        await loc.click();
        log(`click ${describe(flags)}`);
        console.log("clicked");
        break;
      }
      case "fill": {
        if (flags.value === undefined) fail("fill needs --value");
        await locator(page, flags).fill(flags.value);
        log(`fill ${describe(flags)} value=${JSON.stringify(flags.name === "API token" || flags.label === "API token" ? "<token>" : flags.value)}`);
        console.log("filled");
        break;
      }
      case "check": {
        await locator(page, flags).check();
        log(`check ${describe(flags)}`);
        console.log("checked");
        break;
      }
      case "expect": {
        const loc = locator(page, flags);
        try {
          if (flags.absent) await loc.waitFor({ state: "hidden" });
          else await loc.waitFor({ state: "visible" });
          if (flags.value !== undefined) {
            const got = await loc.inputValue();
            if (got !== flags.value) throw new Error(`value is ${JSON.stringify(got)}, want ${JSON.stringify(flags.value)}`);
          }
          if (flags.enabled && !(await loc.isEnabled())) throw new Error("is disabled");
          if (flags.disabled && !(await loc.isDisabled())) throw new Error("is enabled");
          if (flags.checked && !(await loc.isChecked())) throw new Error("is not checked");
          log(`expect PASS ${describe(flags)}${flags.absent ? " absent" : ""}`);
          console.log(`PASS ${describe(flags)}${flags.absent ? " (absent)" : ""}`);
        } catch (e) {
          ok = false;
          log(`expect FAIL ${describe(flags)}: ${String(e.message).split("\n")[0]}`);
          console.error(`FAIL ${describe(flags)}: ${String(e.message).split("\n")[0]}`);
        }
        break;
      }
      case "text": {
        const any = flags.role || flags.text || flags.label || flags.placeholder;
        console.log(await (any ? locator(page, flags) : page.locator("body")).innerText());
        break;
      }
      case "snapshot": {
        const path = flags.path ?? fail("snapshot needs --path");
        mkdirSync(dirname(path), { recursive: true });
        writeFileSync(path, `# ${page.url()}\n${await page.locator("body").ariaSnapshot()}\n`);
        log(`snapshot ${path}`);
        console.log(path);
        break;
      }
      case "screenshot": {
        const path = flags.path ?? fail("screenshot needs --path");
        mkdirSync(dirname(path), { recursive: true });
        await page.screenshot({ path, fullPage: flags.full === true });
        log(`screenshot ${path}`);
        console.log(path);
        break;
      }
      default:
        fail(`unknown command ${cmd}`);
    }
  } catch (e) {
    ok = false;
    log(`${cmd} ERROR ${String(e.message).split("\n")[0]}`);
    console.error(`browser: ${cmd} failed: ${String(e.message).split("\n")[0]}`);
  }
  await browser.close();
  process.exit(ok ? 0 : 1);
}

main();
