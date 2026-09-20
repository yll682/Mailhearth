// Drives a headless Chrome over the DevTools protocol to exercise the SPA
// end-to-end and capture screenshots. Usage:
//   node scripts/screenshot.mjs http://127.0.0.1:8090 out-dir
// Requires Chrome; uses Node's built-in WebSocket (Node >= 22).
import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync, existsSync } from "node:fs";
import { join } from "node:path";

const base = process.argv[2] ?? "http://127.0.0.1:8090";
const out = process.argv[3] ?? "screenshots";
mkdirSync(out, { recursive: true });

const chromePaths = [
  "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
  "C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe",
  "C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe",
  "/usr/bin/google-chrome",
  "/usr/bin/chromium",
];
const chrome = chromePaths.find((p) => existsSync(p));
if (!chrome) throw new Error("no chrome found");
const port = 9333;
const proc = spawn(chrome, [
  "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--hide-scrollbars",
  `--remote-debugging-port=${port}`, "--user-data-dir=" + join(out, ".chrome-profile"), "--window-size=1440,900", "about:blank",
], { stdio: "ignore" });
process.on("exit", () => proc.kill());

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
let targets;
for (let i = 0; i < 50; i++) {
  try {
    targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json();
    if (targets.length) break;
  } catch {}
  await sleep(200);
}
const page = targets.find((t) => t.type === "page");
const ws = new WebSocket(page.webSocketDebuggerUrl);
await new Promise((r) => (ws.onopen = r));
let id = 0;
const pending = new Map();
const events = [];
ws.onmessage = (m) => {
  const msg = JSON.parse(m.data);
  if (msg.id && pending.has(msg.id)) {
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    msg.error ? reject(new Error(msg.error.message)) : resolve(msg.result);
  } else if (msg.method) events.push(msg);
};
const send = (method, params = {}) =>
  new Promise((resolve, reject) => {
    const i = ++id;
    pending.set(i, { resolve, reject });
    ws.send(JSON.stringify({ id: i, method, params }));
  });
await send("Page.enable");
await send("Runtime.enable");
await send("Emulation.setDeviceMetricsOverride", { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });

const evaluate = async (expr) => {
  const r = await send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error("evaluate failed: " + JSON.stringify(r.exceptionDetails).slice(0, 400));
  return r.result.value;
};
const nav = async (url) => {
  await send("Page.navigate", { url });
  await sleep(900);
};
const shot = async (name, fullPage = false) => {
  const r = await send("Page.captureScreenshot", { format: "png", captureBeyondViewport: fullPage });
  writeFileSync(join(out, name + ".png"), Buffer.from(r.data, "base64"));
  console.log("saved", name);
};
const consoleErrors = [];
ws.addEventListener("message", (m) => {
  const msg = JSON.parse(m.data);
  if (msg.method === "Runtime.exceptionThrown") consoleErrors.push(JSON.stringify(msg.params.exceptionDetails).slice(0, 300));
  if (msg.method === "Runtime.consoleAPICalled" && msg.params.type === "error") consoleErrors.push(msg.params.args.map((a) => a.value ?? a.description).join(" "));
});
const waitFor = async (selector, timeout = 8000) => {
  const t0 = Date.now();
  while (Date.now() - t0 < timeout) {
    if (await evaluate(`!!document.querySelector(${JSON.stringify(selector)})`)) return true;
    await sleep(150);
  }
  throw new Error("timeout waiting for " + selector);
};
const api = (method, path, body) =>
  evaluate(`fetch(${JSON.stringify(path)}, {method: ${JSON.stringify(method)}, headers: {"X-Requested-With": "Mailhearth", "Content-Type": "application/json"}, body: ${body ? JSON.stringify(JSON.stringify(body)) : "undefined"}}).then(async r => ({status: r.status, body: await r.text()}))`);

try {
// 1. Setup wizard as shown to a fresh install.
await nav(base + "/");
await waitFor(".auth-card");
await shot("01-setup-org");

// Complete setup through the real UI form (one field at a time, like a person).
const fill = async (selector, value) => {
  await evaluate(`(() => { const el = document.querySelector(${JSON.stringify(selector)}); const proto = Object.getPrototypeOf(el);
    Object.getOwnPropertyDescriptor(proto, "value").set.call(el, ${JSON.stringify(value)}); el.dispatchEvent(new Event("input", {bubbles: true})); })()`);
  await sleep(80);
};
const fields = ["Acme Studio", "Alice Zhang", "alice@acme.test", "correct-horse-battery"];
for (let i = 0; i < fields.length; i++) await fill(`.auth-card form label.field:nth-of-type(${i + 1}) input`, fields[i]);
await evaluate(`document.querySelector(".auth-card form button[type=submit]").click()`);
await sleep(1200);
await waitFor(".steps li.current");
await shot("02-setup-connect");
await fill(".auth-card form input", "dev-token");
await evaluate(`document.querySelector(".auth-card form button[type=submit]").click()`);
await sleep(1500);
await waitFor(".discovery");
await shot("03-setup-import");
await evaluate(`(() => { const s = document.querySelector(".discovery select"); s.value = "alice@acme.test"; s.dispatchEvent(new Event("change", {bubbles: true})); })()`);
await evaluate(`[...document.querySelectorAll(".auth-card button")].find(b => b.textContent.includes("Import") || b.textContent.includes("导入并完成")).click()`);
await sleep(2500);
await shot("04-setup-done");

// 2. Mail client.
await nav(base + "/mail");
await waitFor(".rows .row", 10000);
await sleep(800);
await shot("05-mail-inbox");
await evaluate(`document.querySelector(".rows .row").click()`);
await sleep(2000);
await shot("06-mail-read");
await evaluate(`[...document.querySelectorAll(".reader-toolbar .btn")].find(b => /Reply|回复/.test(b.textContent)).click()`);
await sleep(1200);
await shot("07-mail-reply");
await evaluate(`[...document.querySelectorAll(".composer-head .btn-icon")].pop().click()`);
await sleep(300);
await evaluate(`(() => { const b = [...document.querySelectorAll(".modal-foot .btn")].find(b => /Discard|丢弃/.test(b.textContent)); b && b.click(); })()`);
await sleep(500);

// Search
await evaluate(`(() => { const el = document.querySelector(".search input"); const proto = Object.getPrototypeOf(el); Object.getOwnPropertyDescriptor(proto, "value").set.call(el, "is:unread invoice"); el.dispatchEvent(new Event("input", {bubbles: true})); })()`);
await sleep(1500);
await shot("08-mail-search");

// The ⓘ popover must escape modal and scroll-pane clipping, so check it in
// both a plain pane and inside a modal.
await evaluate(`(() => { const el = document.querySelector(".search input"); const proto = Object.getPrototypeOf(el); Object.getOwnPropertyDescriptor(proto, "value").set.call(el, ""); el.dispatchEvent(new Event("input", {bubbles: true})); })()`);
await sleep(900);
await evaluate(`document.querySelector(".search .info-btn").click()`);
await sleep(350);
const popped = await evaluate(`(() => { const e = document.querySelector(".info-pop"); if (!e) return "missing";
  const r = e.getBoundingClientRect(); return JSON.stringify({ w: Math.round(r.width), onScreen: r.left >= 0 && r.right <= innerWidth && r.top >= 0 }); })()`);
console.log("info popover:", popped);
await shot("08b-info-popover");
await evaluate(`document.querySelector(".search .info-btn").click()`);

// 3. Admin console.
await nav(base + "/admin");
await waitFor(".stat-grid", 10000);
await sleep(600);
await shot("09-admin-overview");
await nav(base + "/admin/members");
await waitFor(".table tbody tr");
await shot("10-admin-members");
await evaluate(`[...document.querySelectorAll(".page-head .btn")].find(b => /Add member|添加成员/.test(b.textContent)).click()`);
await sleep(800);
await shot("11-admin-add-member");
await evaluate(`document.querySelector(".modal-head .btn-icon").click()`);
await nav(base + "/admin/mailboxes");
await waitFor(".table tbody tr");
await shot("12-admin-mailboxes");
await evaluate(`[...document.querySelectorAll(".table tbody tr")].find(r => r.textContent.includes("support@")).click()`);
await sleep(1200);
await shot("13-admin-mailbox-detail");
await nav(base + "/admin/addresses");
await waitFor(".table tbody tr");
await shot("14-admin-addresses");
await nav(base + "/admin/domains");
await waitFor(".card-list .card");
await shot("15-admin-domains");
await nav(base + "/settings/rules");
await waitFor(".page-body");
await sleep(1000);
await shot("16-settings-rules");

// 4. Mobile layout.
await send("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 2, mobile: true });
await nav(base + "/mail");
await waitFor(".rows .row", 10000);
await sleep(800);
await shot("17-mobile-inbox");
await evaluate(`document.querySelector(".rows .row").click()`);
await sleep(1500);
await shot("18-mobile-read");

} catch (e) {
  console.error("FAILED:", e.message);
  await shot("99-failure").catch(() => {});
  console.log("page text:", (await evaluate("document.body.innerText.slice(0, 600)").catch(() => "")));
  ws.close();
  proc.kill();
  process.exit(1);
}
console.log("console errors:", consoleErrors.length ? consoleErrors : "none");
ws.close();
proc.kill();
process.exit(consoleErrors.length ? 2 : 0);
