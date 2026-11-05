// Browser audit of notifications (local Notification API) and accessibility/responsiveness
// of the main panel journeys, on the same local simulation fixture as M4A (stand-in Meta,
// loopback only, synthetic data). No push service, no real provider.
import { expect as playwrightExpect, type Browser, type BrowserContext, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { createHmac, randomUUID } from "node:crypto";
import axe from "axe-core";

const expect = playwrightExpect.configure({ timeout: 20_000 });
const ORIGIN = "https://localhost:5174";

type Actor = { Token: string; CSRF: string; User: string; Member: string; Role: string };
type Fixture = {
  App: string; Secret: string; Waba: string; Phone: string;
  M3B: { Tenant: string; Inbox: string; Actors: Actor[] };
  M4A: { Admin: Actor };
};
type Panel = { page: Page; context: BrowserContext; errors: string[] };

const recordNotifications = `
  window.__notes = [];
  const Real = window.Notification;
  if (Real) window.Notification = new Proxy(Real, { construct(t, args, nt) { window.__notes.push(String(args[0])); return Reflect.construct(t, args, nt); } });
`;
const removeNotification = `delete window.Notification; window.__notes = [];`;
function fakeNotification(initial: string, answer: string, mode: "ok" | "reject" | "ctor-throws") {
  return `
  window.__notes = []; window.__requests = 0; let perm = ${JSON.stringify(initial)};
  function N(title) { if (${JSON.stringify(mode)} === "ctor-throws") throw new TypeError("Illegal constructor"); window.__notes.push(String(title)); }
  Object.defineProperty(N, "permission", { get() { return perm; } });
  N.requestPermission = async () => { window.__requests++; if (${JSON.stringify(mode)} === "reject") throw new Error("blocked"); perm = ${JSON.stringify(answer)}; return perm; };
  window.Notification = N;`;
}

// React key warnings are real defects: siblings may be dropped or duplicated.
const reactKeyDefects: string[] = [];

async function openPanel(browser: Browser, actor: Actor, opts: { init?: string; permissions?: string[]; viewport?: { width: number; height: number }; path?: string } = {}): Promise<Panel> {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: opts.viewport ?? { width: 1280, height: 800 } });
  await context.addCookies([{ name: "__Host-session", value: actor.Token, url: ORIGIN, secure: true, httpOnly: true, sameSite: "Strict" }]);
  if (opts.permissions) await context.grantPermissions(opts.permissions, { origin: ORIGIN });
  if (opts.init) await context.addInitScript({ content: opts.init });
  const page = await context.newPage();
  page.setDefaultTimeout(20_000);
  page.setDefaultNavigationTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error" && /same key|unique "key"/.test(message.text())) {
      const where = new URL(page.url()).pathname;
      void Promise.all(message.args().map((arg) => arg.jsonValue().catch(() => ""))).then((args) => {
        const stack = args.map(String).filter((value) => /\n\s+at /.test(value)).join(" ").split("\n").map((line) => line.trim().split(" ")[1]).filter(Boolean).slice(0, 4).join(" < ");
        const id = String(args[1] ?? "");
        return page.waitForTimeout(300).then(() => page.evaluate((key) => [...document.querySelectorAll("*")]
          .filter((el) => el.getAttribute("value") === key || (el.children.length === 0 && (el.textContent ?? "").includes(key)))
          .map((el) => `${el.tagName.toLowerCase()} em ${el.closest("section")?.getAttribute("aria-label") ?? el.closest("section")?.querySelector("h2")?.textContent ?? "?"}`)
          .slice(0, 3).join(", "), id).catch(() => "")).then((dom) => {
          reactKeyDefects.push(`${where}: chave ${id} em ${stack || dom || "componente desconhecido"}`);
        });
      });
    }
  });
  await page.goto(`${ORIGIN}/entrar`, { waitUntil: "domcontentloaded", timeout: 60_000 });
  await go(page, actor.CSRF, opts.path ?? "/conversas", true);
  return { page, context, errors };
}


// Walks the React fiber tree and names the parent of siblings sharing a key.
async function duplicateKeys(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const root = document.getElementById("root");
    const containerKey = root ? Object.keys(root).find((key) => key.startsWith("__reactContainer$")) : undefined;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const start: any = containerKey && root ? (root as any)[containerKey] : null;
    const out: string[] = [];
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const name = (fiber: any): string => { let f = fiber; while (f) { const t = f.type; if (typeof t === "function" && (t.displayName || t.name)) return t.displayName || t.name; f = f.return; } return "?"; };
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const walk = (fiber: any, depth: number) => {
      if (!fiber || depth > 400) return;
      const seen = new Map<string, number>();
      let child = fiber.child;
      while (child) { if (child.key != null) seen.set(child.key, (seen.get(child.key) ?? 0) + 1); child = child.sibling; }
      for (const [key, count] of seen) if (count > 1) out.push(`${name(fiber)} (${typeof fiber.type === "string" ? fiber.type : "componente"}) repete ${key} ${count}x`);
      child = fiber.child;
      while (child) { walk(child, depth + 1); child = child.sibling; }
    };
    walk(start, 0);
    return out;
  });
}

async function go(page: Page, csrf: string, path: string, remember = false) {
  await page.evaluate(async ({ csrf, path, remember }) => {
    if (remember) {
      const response = await fetch("/api/panel/session");
      if (!response.ok) throw new Error(`fixture session: ${response.status}`);
      const data = await response.json();
      const { rememberSession } = await import(/* @vite-ignore */ "/src/api/panel.ts");
      rememberSession({ ...data, csrf_token: csrf });
    }
    window.history.pushState({}, "", path);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, { csrf, path, remember });
}

async function bindInbox(page: Page, inbox: string) {
  await page.getByRole("combobox", { name: "Caixa", exact: true }).selectOption(inbox);
  await page.getByRole("tab", { name: "Todas", exact: true }).click();
}
function rows(page: Page) { return page.getByRole("list", { name: "Conversas encontradas" }).getByRole("button"); }

async function receive(fixture: Fixture, from: string, text: string) {
  const body = JSON.stringify({
    object: "whatsapp_business_account",
    entry: [{ id: fixture.Waba, changes: [{ field: "messages", value: {
      messaging_product: "whatsapp",
      metadata: { phone_number_id: fixture.Phone, display_phone_number: fixture.Phone },
      contacts: [{ profile: { name: "Cliente Simulado" }, wa_id: from }],
      messages: [{ from, id: `wamid.simulation.${randomUUID()}`, timestamp: String(Math.floor(Date.now() / 1000)), type: "text", text: { body: text } }],
    } }] }],
  });
  const response = await fetch(`http://127.0.0.1:${process.env.CHATISP_E2E_API_PORT ?? "8080"}/api/webhooks/meta?app_id=${encodeURIComponent(fixture.App)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Hub-Signature-256": `sha256=${createHmac("sha256", fixture.Secret).update(body).digest("hex")}`, "User-Agent": "chatisp-metacloud-simulation" },
    body,
  });
  expect(response.status).toBe(200);
}

// Turns the browser preference on and silent mode off on the settings page, then returns to the queue.
async function enableBrowserPreference(panel: Panel, actor: Actor, inbox: string) {
  await go(panel.page, actor.CSRF, "/configuracoes");
  const prefs = panel.page.getByRole("region", { name: "Preferências de notificações" });
  await expect(prefs).toBeVisible();
  const browserBox = prefs.getByRole("checkbox", { name: "Notificações do navegador" });
  const silent = prefs.getByRole("checkbox", { name: "Modo silencioso" });
  if (await silent.isChecked()) { await silent.click(); await expect(silent).not.toBeChecked(); }
  if (!(await browserBox.isChecked())) { await browserBox.click(); await expect(browserBox).toBeChecked(); }
  await expect(silent).not.toBeChecked();
  await go(panel.page, actor.CSRF, "/conversas");
  await bindInbox(panel.page, inbox);
}

const notes = (page: Page) => page.evaluate(() => (window as unknown as { __notes: string[] }).__notes);
const permissionText = (page: Page) => page.getByRole("region", { name: "Preferências de notificações" }).locator("p").first();

type Violation = { id: string; impact: string | null; help: string; nodes: number; targets: string[]; html: string[] };
async function runAxe(page: Page, tags: string[]): Promise<Violation[]> {
  await page.addScriptTag({ content: axe.source });
  return page.evaluate(async (tags) => {
    const result = await (window as unknown as { axe: { run: (c: Document, o: unknown) => Promise<{ violations: { id: string; impact: string | null; help: string; nodes: { target: unknown[] }[] }[] }> } }).axe.run(document, { runOnly: { type: "tag", values: tags } });
    return result.violations.map((v) => ({ id: v.id, impact: v.impact, help: v.help, nodes: v.nodes.length, targets: v.nodes.slice(0, 4).map((n) => n.target.join(" ")), html: v.nodes.slice(0, 2).map((n) => (n as unknown as { html: string }).html.slice(0, 200)) }));
  }, tags);
}

async function keyboardWalk(page: Page, max = 70) {
  await page.addStyleTag({ content: "*,*::before,*::after{transition:none !important;animation:none !important}" });
  await page.evaluate(() => { document.querySelectorAll("[data-ux-seen]").forEach((e) => e.removeAttribute("data-ux-seen")); (document.activeElement as HTMLElement | null)?.blur(); });
  const problems: string[] = [];
  const order: string[] = [];
  for (let i = 0; i < max; i++) {
    await page.keyboard.press("Tab");
    const info = await page.evaluate(() => {
      const el = document.activeElement as HTMLElement | null;
      if (!el || el === document.body) return { end: true as const };
      if (el.hasAttribute("data-ux-seen")) return { end: true as const };
      el.setAttribute("data-ux-seen", "1");
      const cs = getComputedStyle(el);
      const rect = el.getBoundingClientRect();
      const labelled = (el as HTMLInputElement).labels?.[0]?.textContent ?? "";
      const name = (el.getAttribute("aria-label") || labelled || el.textContent || (el as HTMLInputElement).placeholder || el.getAttribute("title") || "").trim().replace(/\s+/g, " ").slice(0, 40);
      const outline = cs.outlineStyle !== "none" && parseFloat(cs.outlineWidth) > 0;
      const shadow = cs.boxShadow !== "none";
      return { end: false as const, tag: el.tagName.toLowerCase(), name, visible: rect.width > 0 && rect.height > 0, ring: outline || shadow };
    });
    if (info.end) { if (order.length > 0) break; continue; }
    order.push(`${info.tag}:${info.name}`);
    if (!info.ring) problems.push(`sem indicador de foco: <${info.tag}> "${info.name}"`);
    if (!info.visible) problems.push(`foco em elemento invisível: <${info.tag}> "${info.name}"`);
    if (!info.name) problems.push(`foco em controle sem nome: <${info.tag}>`);
  }
  return { reached: order.length, problems, order };
}

async function overflow(page: Page) {
  return page.evaluate(() => {
    const width = window.innerWidth;
    const wide: string[] = [];
    for (const el of Array.from(document.querySelectorAll<HTMLElement>("body *"))) {
      const r = el.getBoundingClientRect();
      if (r.width > 0 && r.right > width + 1 && getComputedStyle(el).position !== "fixed") {
        // Ignore content that scrolls inside its own overflow container.
        let p: HTMLElement | null = el.parentElement; let clipped = false;
        while (p && p !== document.body) { const o = getComputedStyle(p).overflowX; if (o === "auto" || o === "scroll" || o === "hidden") { clipped = true; break; } p = p.parentElement; }
        if (!clipped) { const label = el.getAttribute("aria-label") || (el as HTMLInputElement).labels?.[0]?.textContent?.trim().slice(0, 30) || (el.textContent ?? "").trim().slice(0, 30); wide.push(`${el.tagName.toLowerCase()}${el.id ? "#" + el.id : ""}[${label}] (${Math.round(r.right)}px)`); }
      }
    }
    return { page: document.documentElement.scrollWidth - width, wide: wide.slice(0, 6) };
  });
}

const WCAG = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"];

export async function runUx(browser: Browser, testInfo: { attach(name: string, options: { body: string; contentType: string }): Promise<void> }) {
  const filename = process.env.M4A_FIXTURE_FILE;
  if (!filename) throw new Error("Fixture ausente: execute npm run test:e2e:ux; somente localhost. Sem skip.");
  const fixture: Fixture = JSON.parse(await readFile(filename, "utf8"));
  const agentActor = fixture.M3B.Actors[0]!;
  const admin = fixture.M4A.Admin;
  const inbox = fixture.M3B.Inbox;
  const evidence: Record<string, unknown> = {};
  const discover = process.env.UX_DISCOVER === "1";
  const failures: string[] = [];

  // ---------- 1. Notificações locais (Notification API; não é push) ----------
  // 1a. Permissão concedida de verdade (Chromium): a notificação é criada, sem texto do cliente.
  const granted = await openPanel(browser, agentActor, { permissions: ["notifications"], init: recordNotifications });
  try {
    await bindInbox(granted.page, inbox);
    await go(granted.page, agentActor.CSRF, "/configuracoes");
    await expect(permissionText(granted.page)).toHaveText("Permissão concedida.");
    await expect(granted.page.getByRole("button", { name: "Permitir notificações" })).toHaveCount(0);
    await enableBrowserPreference(granted, agentActor, inbox);
    await receive(fixture, "5511977770001", "texto-sigiloso-do-cliente-ux");
    await expect.poll(() => notes(granted.page)).toContain("Nova conversa");
    await receive(fixture, "5511977770001", "segunda-mensagem-sigilosa");
    await expect.poll(async () => (await notes(granted.page)).includes("Nova mensagem"), { message: "Nova mensagem após a segunda mensagem" }).toBe(true);
    const all = (await notes(granted.page)).join("|");
    expect(all).not.toContain("sigilos");
    expect(all).not.toContain("5511977770001");
    evidence.granted = { notes: await notes(granted.page), errors: granted.errors };
    expect(granted.errors).toEqual([]);
  } finally { await granted.context.close(); }

  // 1b. Sem concessão (estado padrão do Chromium headless): nada é criado e a interface diz o estado real.
  const denied = await openPanel(browser, agentActor, { init: recordNotifications });
  try {
    const realPermission = await denied.page.evaluate(() => Notification.permission);
    evidence.deniedInitialPermission = realPermission;
    await go(denied.page, agentActor.CSRF, "/configuracoes");
    if (realPermission === "default") {
      await denied.page.getByRole("button", { name: "Permitir notificações" }).click();
      await expect(permissionText(denied.page)).toHaveText(/Permissão (recusada no navegador|ainda não solicitada)\./);
    } else {
      await expect(permissionText(denied.page)).toHaveText("Permissão recusada no navegador.");
    }
    evidence.deniedFinalText = await permissionText(denied.page).innerText();
    await enableBrowserPreference(denied, agentActor, inbox);
    await receive(fixture, "5511977770002", "mensagem-sem-permissao");
    await expect(rows(denied.page).filter({ hasText: "5511977770002" }).or(rows(denied.page).first())).toBeVisible();
    await denied.page.waitForTimeout(1500);
    expect(await notes(denied.page)).toEqual([]);
    expect(denied.errors).toEqual([]);
  } finally { await denied.context.close(); }

  // 1c. Sem suporte (API ausente): aviso claro, nenhum erro, a fila segue funcionando.
  const missing = await openPanel(browser, agentActor, { init: removeNotification });
  try {
    expect(await missing.page.evaluate(() => typeof Notification)).toBe("undefined");
    await go(missing.page, agentActor.CSRF, "/configuracoes");
    await expect(permissionText(missing.page)).toHaveText("Notificações indisponíveis neste navegador.");
    await expect(missing.page.getByRole("button", { name: "Permitir notificações" })).toHaveCount(0);
    await enableBrowserPreference(missing, agentActor, inbox);
    await receive(fixture, "5511977770003", "mensagem-sem-api");
    await expect(rows(missing.page).first()).toBeVisible();
    await missing.page.waitForTimeout(1000);
    expect(missing.errors).toEqual([]);
  } finally { await missing.context.close(); }

  // 1d. Fluxo de pedido de permissão (diálogo não automatizável em headless: API simulada, uma só vez por ação).
  for (const [answer, text] of [["granted", "Permissão concedida."], ["denied", "Permissão recusada no navegador."]] as const) {
    const prompt = await openPanel(browser, agentActor, { init: fakeNotification("default", answer, "ok") });
    try {
      await go(prompt.page, agentActor.CSRF, "/configuracoes");
      await expect(permissionText(prompt.page)).toHaveText("Permissão ainda não solicitada.");
      expect(await prompt.page.evaluate(() => (window as unknown as { __requests: number }).__requests)).toBe(0);
      await prompt.page.getByRole("button", { name: "Permitir notificações" }).click();
      await expect(permissionText(prompt.page)).toHaveText(text);
      expect(await prompt.page.evaluate(() => (window as unknown as { __requests: number }).__requests)).toBe(1);
      if (answer === "granted") {
        await enableBrowserPreference(prompt, agentActor, inbox);
        await receive(fixture, `55119777700${answer === "granted" ? "10" : "11"}`, "pos-permissao");
        await expect.poll(() => notes(prompt.page)).toContain("Nova conversa");
      } else {
        await expect(prompt.page.getByRole("button", { name: "Permitir notificações" })).toHaveCount(0);
      }
    } finally { await prompt.context.close(); }
  }
  const rejected = await openPanel(browser, agentActor, { init: fakeNotification("default", "granted", "reject") });
  try {
    await go(rejected.page, agentActor.CSRF, "/configuracoes");
    await rejected.page.getByRole("button", { name: "Permitir notificações" }).click();
    await expect(rejected.page.getByRole("alert").filter({ hasText: "Não foi possível solicitar permissão ao navegador." })).toBeVisible();
    await expect(permissionText(rejected.page)).toHaveText("Permissão ainda não solicitada.");
  } finally { await rejected.context.close(); }
  // Construtor que lança (ex.: navegadores móveis exigem ServiceWorker): a fila continua.
  const throwing = await openPanel(browser, agentActor, { init: fakeNotification("granted", "granted", "ctor-throws") });
  try {
    await enableBrowserPreference(throwing, agentActor, inbox);
    await receive(fixture, "5511977770012", "construtor-lanca");
    await expect(rows(throwing.page).first()).toBeVisible();
    await throwing.page.waitForTimeout(1000);
    expect(throwing.errors).toEqual([]);
  } finally { await throwing.context.close(); }

  // ---------- 2. Acessibilidade e responsividade ----------
  // 320x256 CSS px is the WCAG 1.4.10 reflow target (1280 px at 400% zoom).
  const viewports = [{ name: "desktop 1280x800", width: 1280, height: 800 }, { name: "tablet 768x1024", width: 768, height: 1024 }, { name: "celular 360x640", width: 360, height: 640 }, { name: "reflow 320x256 (zoom 400%)", width: 320, height: 256 }];
  const audit: Record<string, unknown>[] = [];
  for (const vp of viewports) {
    const agentPanel = await openPanel(browser, agentActor, { viewport: vp });
    const adminPanel = await openPanel(browser, admin, { viewport: vp });
    try {
      await bindInbox(agentPanel.page, inbox);
      await expect(rows(agentPanel.page).first()).toBeVisible();
      await rows(agentPanel.page).first().click();
      await expect(agentPanel.page.getByRole("textbox", { name: "Mensagem", exact: true })).toBeVisible();
      const stops: { label: string; page: Page; csrf: string; path?: string }[] = [
        { label: "conversas (conversa aberta)", page: agentPanel.page, csrf: agentActor.CSRF },
        { label: "configurações", page: adminPanel.page, csrf: admin.CSRF, path: "/configuracoes" },
        { label: "contatos", page: adminPanel.page, csrf: admin.CSRF, path: "/contatos" },
        { label: "equipes", page: adminPanel.page, csrf: admin.CSRF, path: "/equipes" },
        { label: "relatórios", page: adminPanel.page, csrf: admin.CSRF, path: "/relatorios" },
        { label: "painel", page: adminPanel.page, csrf: admin.CSRF, path: "/painel" },
        { label: "canais", page: adminPanel.page, csrf: admin.CSRF, path: "/configuracoes/canais" },
        { label: "usuários do provedor", page: adminPanel.page, csrf: admin.CSRF, path: "/configuracoes/usuarios" },
      ];
      for (const stop of stops) {
        if (stop.path) { await go(stop.page, stop.csrf, stop.path); await stop.page.waitForTimeout(900); }
        for (const dup of await duplicateKeys(stop.page)) failures.push(`${vp.name} / ${stop.label}: ${dup}`);
        const violations = await runAxe(stop.page, WCAG);
        const over = await overflow(stop.page);
        const keys = await keyboardWalk(stop.page);
        const entry = { viewport: vp.name, page: stop.label, violations, overflowPx: over.page, wide: over.wide, tabStops: keys.reached, focusProblems: keys.problems, order: keys.order.slice(0, 12) };
        audit.push(entry);
        if (violations.length) failures.push(`${vp.name} / ${stop.label}: axe ${violations.map((v) => `${v.id}(${v.nodes}) ${v.targets[0] ?? ""} <${v.html[0] ?? ""}>`).join("; ")}`);
        if (over.page > 0 || over.wide.length) failures.push(`${vp.name} / ${stop.label}: overflow horizontal ${over.page}px ${over.wide.join(", ")}`);
        if (keys.problems.length) failures.push(`${vp.name} / ${stop.label}: teclado ${keys.problems.slice(0, 4).join("; ")}`);
        if (keys.reached < 2) failures.push(`${vp.name} / ${stop.label}: só ${keys.reached} parada(s) de Tab`);
      }
    } finally { await agentPanel.context.close(); await adminPanel.context.close(); }
  }
  // Telas sem sessão: entrada, recuperação de senha, convite e MFA.
  const publicPages = [
    { label: "entrar", path: "/entrar", heading: "Entrar" },
    { label: "recuperar senha", path: "/recuperar-senha", heading: "Recuperar senha" },
    { label: "confirmar nova senha", path: "/recuperar-senha/confirmar?token=sintetico-invalido", heading: "Confirmar nova senha" },
    { label: "aceitar convite", path: "/convite?token=sintetico-invalido", heading: "Aceitar convite" },
    { label: "mfa", path: "/mfa", heading: "" },
  ];
  for (const vp of viewports) {
    const context = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: vp.width, height: vp.height } });
    const page = await context.newPage();
    try {
      for (const pub of publicPages) {
        await page.goto(`${ORIGIN}${pub.path}`, { waitUntil: "domcontentloaded", timeout: 60_000 });
        const h1 = page.getByRole("heading", { level: 1 });
        if (pub.heading) await expect(page.getByRole("heading", { name: pub.heading, level: 1 })).toBeVisible();
        else await expect(h1.first()).toBeVisible();
        await page.waitForTimeout(400);
        const shown = (await h1.first().textContent())?.trim() ?? "";
        const violations = await runAxe(page, WCAG);
        const over = await overflow(page);
        const keys = await keyboardWalk(page, 12);
        audit.push({ viewport: vp.name, page: pub.label, heading: shown, url: new URL(page.url()).pathname, violations, overflowPx: over.page, wide: over.wide, tabStops: keys.reached, focusProblems: keys.problems });
        if (violations.length) failures.push(`${vp.name} / ${pub.label}: axe ${violations.map((v) => `${v.id}(${v.nodes}) ${v.targets[0] ?? ""}`).join("; ")}`);
        if (over.page > 0 || over.wide.length) failures.push(`${vp.name} / ${pub.label}: overflow ${over.page}px ${over.wide.join(", ")}`);
        if (keys.problems.length) failures.push(`${vp.name} / ${pub.label}: teclado ${keys.problems.join("; ")}`);
        if (keys.reached < 1) failures.push(`${vp.name} / ${pub.label}: nenhuma parada de Tab`);
      }
    } finally { await context.close(); }
  }
  evidence.audit = audit;
  for (const defect of [...new Set(reactKeyDefects)]) failures.push(`chave React duplicada em ${defect}`);
  await testInfo.attach("ux-audit", { body: JSON.stringify(evidence, null, 2), contentType: "application/json" });
  console.log(JSON.stringify({ auditedViews: audit.length, failures: failures.length }));
  if (failures.length) {
    const text = `Achados de acessibilidade/responsividade (${failures.length}):\n- ${failures.join("\n- ")}`;
    if (discover) console.log(text);
    else throw new Error(text);
  }
}
