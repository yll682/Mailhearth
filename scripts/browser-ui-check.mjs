import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.MAILHEARTH_PLAYWRIGHT_PATH);
const base = process.env.MAILHEARTH_UI_URL ?? "http://127.0.0.1:18081";
const browser = await chromium.launch({ executablePath: process.env.MAILHEARTH_BROWSER_PATH, headless: true });
try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 720 }, locale: "en-GB" });
  context.setDefaultTimeout(15000);
  const login = await context.request.post(`${base}/api/auth/login`, {
    headers: { "X-Requested-With": "Mailhearth" },
    data: { email: "admin@acme.test", password: "Mailhearth-Demo-2026!" },
  });
  assert.equal(login.status(), 200, "独立演示管理员登录失败");
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(`${base}/admin/domains`);
  await page.locator(".card-list .card").first().waitFor();
  assert.equal(await page.locator(".card-list .card").count(), 3, "域名页面应显示三个域名");
  assert.deepEqual(errors, [], "域名页面出现 JavaScript 错误");
  console.log("Domains 页面检查通过：三个域名已经显示，没有 JavaScript 错误。");
  const ready = async (path) => {
    await page.goto(base + path);
    await page.locator(".page").waitFor();
    await page.waitForFunction(() => !document.querySelector(".page .spinner"));
  };
  const scroll = async (selector) => {
    const element = page.locator(selector).first();
    const dimensions = await element.evaluate((node) => {
      const rect = node.getBoundingClientRect();
      return { height: node.clientHeight, content: node.scrollHeight, bottom: rect.bottom, viewport: innerHeight, overflow: getComputedStyle(node).overflowY };
    });
    assert.ok(dimensions.height > 0, `${selector} 没有可用高度`);
    assert.ok(dimensions.bottom <= dimensions.viewport + 1, `${selector} 超出窗口高度`);
    if (dimensions.content > dimensions.height + 1) {
      assert.ok(["auto", "scroll"].includes(dimensions.overflow), `${selector} 未启用滚动`);
      const position = await element.evaluate((node) => { node.scrollTop = node.scrollHeight; return node.scrollTop; });
      assert.ok(position > 0, `${selector} 无法滚动`);
      await element.evaluate((node) => { node.scrollTop = 0; });
    }
  };
  const modal = async (path, button) => {
    await ready(path);
    await page.getByRole("button", { name: button, exact: true }).first().click();
    await page.getByRole("dialog").waitFor();
    await scroll(".modal-body");
    const last = page.locator(".modal-body button").last();
    if (await last.count()) {
      await last.scrollIntoViewIfNeeded();
      const rect = await last.boundingBox();
      assert.ok(rect && rect.y >= 0 && rect.y + rect.height <= (await page.evaluate(() => innerHeight)), "弹窗最后的按钮无法到达");
    }
    await page.keyboard.press("Escape");
    await page.getByRole("dialog").waitFor({ state: "hidden" });
  };
  const reachable = async (element) => {
    await element.scrollIntoViewIfNeeded();
    const rect = await element.boundingBox();
    const viewport = page.viewportSize();
    assert.ok(rect && rect.x >= -1 && rect.y >= -1 && rect.x + rect.width <= viewport.width + 1 && rect.y + rect.height <= viewport.height + 1, "操作控件无法到达");
    await page.waitForFunction((node) => {
      const rect = node.getBoundingClientRect();
      const target = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
      return target === node || node.contains(target);
    }, await element.elementHandle());
  };
  for (const viewport of [{ width: 1280, height: 720 }, { width: 900, height: 360 }, { width: 390, height: 844 }, { width: 390, height: 320 }, { width: 320, height: 480 }]) {
    await page.setViewportSize(viewport);
    for (const section of ["overview", "members", "mailboxes", "addresses", "groups", "domains", "roles", "audit", "connections", "operations", "organisation"]) {
      await ready("/admin/" + section);
      await scroll(".admin-main");
    }
    for (const tab of ["account", "identities", "rules", "folders", "submissions"]) {
      await ready("/settings/" + tab);
      await scroll(".settings-page");
    }
    await reachable(page.getByRole("tab", { name: "Sending requests", exact: true }));
    await modal("/admin/connections", "Add connection");
    await modal("/admin/members", "Add member");
    await modal("/admin/domains", "Add domain");
    await ready("/admin/connections");
    await page.getByRole("button", { name: "Discover resources", exact: true }).first().click();
    await page.getByRole("dialog").waitFor();
    await page.getByRole("dialog").getByText(/support@acme.test → carol@acme.test/).waitFor();
    await scroll(".modal-body");
    await reachable(page.getByRole("button", { name: "Import selected resources", exact: true }));
    await page.keyboard.press("Escape");
    if (viewport.width <= 860) await page.getByRole("button", { name: "Admin", exact: true }).click();
    await scroll(".sidebar");
    await reachable(page.locator(".sidebar .folder").last());
    await reachable(page.locator(".sidebar .user-btn"));
    await page.goto(base + "/mail");
    await page.locator(".rows .row").first().waitFor();
    await scroll(".msg-list");
    const mailboxResponse = await context.request.get(base + "/api/mail/mailboxes");
    const boxes = await mailboxResponse.json();
    const personal = boxes.find((box) => box.address === "alice@acme.test");
    await page.goto(`${base}/mail/${personal.id}/INBOX/24`);
    await page.locator(".reader-scroll .reader-subject").waitFor();
    await scroll(".reader-scroll");
    if (viewport.width <= 860) {
      await page.goto(base + "/mail");
      await page.locator(".rows .row").first().waitFor();
      await page.getByRole("button", { name: "Folders", exact: true }).click();
    }
    await scroll(".sidebar");
    await reachable(page.getByRole("button", { name: "New folder", exact: true }));
    await reachable(page.locator(".sidebar .user-btn"));
    await page.getByRole("button", { name: "Compose", exact: true }).click();
    await page.locator(".composer").waitFor();
    await page.getByRole("button", { name: "Cc", exact: true }).click();
    await page.getByRole("button", { name: "Bcc", exact: true }).click();
    await scroll(".composer-body");
    await reachable(page.locator(".composer-foot").getByRole("button", { name: "Send", exact: true }));
    const footer = await page.locator(".composer-foot").boundingBox();
    assert.ok(footer && footer.y >= 0 && footer.y + footer.height <= viewport.height + 1, "写邮件窗口的发送按钮超出窗口");
    await page.goto(base + "/admin/domains");
    assert.deepEqual(errors, [], "页面出现 JavaScript 错误");
    console.log(`页面与弹窗滚动检查通过：${viewport.width} × ${viewport.height}`);
  }
  const loggedOut = await browser.newContext({ viewport: { width: 390, height: 320 }, locale: "en-GB" });
  const auth = await loggedOut.newPage();
  await auth.goto(base + "/login");
  await auth.locator(".auth-card").waitFor();
  const authHeight = await auth.locator(".auth-page").evaluate((node) => ({ height: node.clientHeight, content: node.scrollHeight }));
  assert.ok(authHeight.content > authHeight.height, "登录页面检查需要长内容");
  await auth.locator(".lang-switch button").last().scrollIntoViewIfNeeded();
  const setupBase = process.env.MAILHEARTH_SETUP_UI_URL;
  assert.ok(setupBase, "需要独立初始化环境的地址");
  const setupStatus = await loggedOut.request.get(setupBase + "/api/setup/status");
  if ((await setupStatus.json()).step === "org") {
    const created = await loggedOut.request.post(setupBase + "/api/setup/init", {
      headers: { "X-Requested-With": "Mailhearth" },
      data: { orgName: "浏览器滚动检查", adminName: "滚动检查管理员", adminEmail: "scroll@example.test", password: "Mailhearth-Scroll-2026!" },
    });
    assert.equal(created.status(), 200);
  } else {
    const signedIn = await loggedOut.request.post(setupBase + "/api/auth/login", {
      headers: { "X-Requested-With": "Mailhearth" }, data: { email: "scroll@example.test", password: "Mailhearth-Scroll-2026!" },
    });
    assert.equal(signedIn.status(), 200);
  }
  await auth.goto(setupBase + "/setup");
  await auth.getByRole("button", { name: "Save connection", exact: true }).waitFor();
  for (const viewport of [{ width: 1280, height: 720 }, { width: 390, height: 320 }, { width: 320, height: 480 }]) {
    await auth.setViewportSize(viewport);
    await auth.locator(".auth-page").evaluate((node) => { node.scrollTop = 0; });
    const top = await auth.locator(".auth-card").boundingBox();
    assert.ok(top && top.y >= 0, "初始化表单顶部无法到达");
    await auth.getByRole("button", { name: "Save connection", exact: true }).scrollIntoViewIfNeeded();
    const bottom = await auth.getByRole("button", { name: "Save connection", exact: true }).boundingBox();
    assert.ok(bottom && bottom.y >= 0 && bottom.y + bottom.height <= viewport.height, "初始化提交按钮无法到达");
  }
  console.log("登录与初始化向导滚动检查通过。");
} finally {
  await browser.close();
}
