// Jornada integrada curta contra o pacote instalado pelo Compose (scripts/compose-validate.sh).
// Navegador real (Chromium) na origem do laboratório https://localhost:<porta>, com o certificado
// da CA interna do Caddy fixado por SPKI (nada de ignoreHTTPSErrors) e o storage em
// https://storage.localhost:<porta>. Chamadas de API fora do navegador usam Node com a raiz da CA
// do Caddy como única âncora de confiança.
//
// Doubles identificados: SMTP descartável (backend/cmd/smtpsink) e o remetente do webhook da Meta
// (este script, assinando com o segredo do app que ele mesmo cadastra). Banco, Garage, Caddy, API
// e worker são os serviços reais do Compose. Nenhuma chamada externa.
//
// Fases (JOURNEY_PHASE): "before" (jornada completa), "storage-down", "storage-up", "after"
// (depois do reinício dos serviços). Estado entre fases em JOURNEY_STATE.
import { chromium } from "@playwright/test";
import assert from "node:assert/strict";
import { createHash, createHmac, randomBytes, randomUUID } from "node:crypto";
import zlib from "node:zlib";
import { readFile, writeFile } from "node:fs/promises";
import https from "node:https";
import http from "node:http";
import { access, mkdir } from "node:fs/promises";
import { constants } from "node:fs";
import { homedir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const need = (name) => {
  const value = process.env[name];
  if (!value) throw new Error(`${name} ausente`);
  return value;
};
const BASE = need("JOURNEY_BASE");
const STORAGE = need("JOURNEY_STORAGE_BASE");
const SINK = need("JOURNEY_SINK");
const PIN = need("JOURNEY_SPKI");
const CA = await readFile(need("JOURNEY_CA_FILE"));
const STATE_FILE = need("JOURNEY_STATE");
const PHASE = process.env.JOURNEY_PHASE ?? "before";
const ADMIN_EMAIL = need("JOURNEY_ADMIN_EMAIL");
const ADMIN_PASSWORD = need("JOURNEY_ADMIN_PASSWORD");
const root = fileURLToPath(new URL("../../", import.meta.url));

const log = (message) => console.log(`jornada[${PHASE}] ${message}`);
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// ---------- TOTP (RFC 6238, SHA-1, 6 dígitos, 30 s) ----------
function base32(input) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const ch of input.replace(/=+$/, "").toUpperCase()) bits += alphabet.indexOf(ch).toString(2).padStart(5, "0");
  const bytes = [];
  for (let i = 0; i + 8 <= bits.length; i += 8) bytes.push(parseInt(bits.slice(i, i + 8), 2));
  return Buffer.from(bytes);
}
const totpAt = (secret, step) => {
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(step));
  const mac = createHmac("sha1", base32(secret)).update(counter).digest();
  const offset = mac[mac.length - 1] & 15;
  const value = ((mac[offset] & 0x7f) << 24) | (mac[offset + 1] << 16) | (mac[offset + 2] << 8) | mac[offset + 3];
  return String(value % 1_000_000).padStart(6, "0");
};
const lastStep = new Map();
// Um código por janela de 30 s: espera a próxima janela se esta conta já usou a atual.
async function freshCode(account, secret) {
  for (;;) {
    const step = Math.floor(Date.now() / 30_000);
    if ((lastStep.get(account) ?? 0) < step) {
      lastStep.set(account, step);
      state.lastStep = Object.fromEntries(lastStep);
      return totpAt(secret, step);
    }
    await sleep(1000);
  }
}

// ---------- HTTP fora do navegador: só a CA do Caddy é confiável ----------
function request(url, { method = "GET", headers = {}, body } = {}) {
  const target = new URL(url);
  const lib = target.protocol === "https:" ? https : http;
  return new Promise((resolve, reject) => {
    const req = lib.request(
      target,
      {
        method,
        headers,
        ca: CA,
        // *.localhost não resolve em todo glibc; o destino é sempre o loopback publicado.
        lookup: (_host, _opts, cb) => cb(null, [{ address: "127.0.0.1", family: 4 }]),
        servername: target.hostname,
        timeout: 30_000,
      },
      (res) => {
        const chunks = [];
        res.on("data", (c) => chunks.push(c));
        res.on("end", () => resolve({ status: res.statusCode ?? 0, headers: res.headers, body: Buffer.concat(chunks) }));
      },
    );
    req.on("error", reject);
    req.on("timeout", () => req.destroy(new Error("tempo esgotado")));
    if (body !== undefined) req.write(body);
    req.end();
  });
}
const json = (res) => JSON.parse(res.body.toString("utf8"));

async function sinkMessages() {
  return json(await request(`${SINK}/messages`));
}
async function waitMail(to, subjectPart, after = 0) {
  for (let i = 0; i < 60; i++) {
    const found = (await sinkMessages()).filter((m) => m.to === to && m.subject.includes(subjectPart));
    if (found.length > after) return found[found.length - 1];
    await sleep(500);
  }
  throw new Error(`e-mail "${subjectPart}" para ${to} não chegou ao SMTP descartável`);
}
function linkAndToken(message) {
  const link = message.text.split("\n").find((l) => l.startsWith("Abra: "))?.slice(6);
  const token = message.text.trim().split("\n").pop();
  assert.ok(link && token, "e-mail sem link ou token");
  const parsed = new URL(link);
  assert.equal(parsed.origin, BASE, "link do e-mail fora da origem do painel");
  assert.equal(parsed.searchParams.get("token"), token, "link e token divergem");
  return { link, token };
}

// ---------- navegador ----------
async function resolveChromium() {
  const homeCache = path.join(homedir(), ".cache/ms-playwright");
  const candidates = [
    process.env.M3B_CHROMIUM_EXECUTABLE,
    path.join(root, ".paseo-build-tmp/browser/chromium-launcher"),
    chromium.executablePath(),
    path.join(homeCache, "chromium-1193/chrome-linux/chrome"),
    path.join(homeCache, "chromium_headless_shell-1193/chrome-linux/headless_shell"),
  ].filter(Boolean);
  for (const candidate of candidates) {
    try {
      await access(candidate, constants.X_OK);
      return candidate;
    } catch {
      /* próximo */
    }
  }
  throw new Error("Chromium ausente; defina M3B_CHROMIUM_EXECUTABLE");
}
const browser = await chromium.launch({
  executablePath: await resolveChromium(),
  args: [
    `--ignore-certificate-errors-spki-list=${PIN}`,
    "--disable-gpu",
    "--disable-dev-shm-usage",
    "--mute-audio",
    // O cgroup do runner limita threads; um renderizador e um raster bastam (mesmo orçamento dos E2E M3B/UX).
    "--renderer-process-limit=1",
    "--num-raster-threads=1",
    "--disable-features=AudioServiceOutOfProcess,IsolateOrigins,site-per-process",
    "--host-resolver-rules=MAP localhost 127.0.0.1, MAP storage.localhost 127.0.0.1",
  ],
});
const contexts = [];
async function openContext() {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  contexts.push(context);
  const page = await context.newPage();
  page.setDefaultTimeout(30_000);
  const problems = [];
  const sockets = [];
  page.on("websocket", (socket) => {
    const frames = [];
    sockets.push({ socket, frames });
    socket.on("framereceived", ({ payload }) => frames.push(String(payload)));
  });
  page.on("pageerror", (error) => problems.push(error.message));
  page.on("response", (response) => {
    if (response.status() >= 500 && response.url().startsWith(BASE)) problems.push(`${response.status()} ${response.url()}`);
  });
  return { context, page, problems, sockets };
}
async function cookieHeader(context) {
  const cookies = await context.cookies(BASE);
  return {
    cookie: cookies.map((c) => `${c.name}=${c.value}`).join("; "),
    csrf: cookies.find((c) => c.name === "__Host-csrf")?.value ?? "",
  };
}
async function api(context, method, pathname, payload, { raw = false } = {}) {
  const { cookie, csrf } = await cookieHeader(context);
  const headers = { cookie, origin: BASE, "x-csrf-token": csrf };
  let body;
  if (raw) {
    headers["content-type"] = "application/octet-stream";
    body = payload;
  } else if (payload !== undefined) {
    headers["content-type"] = "application/json";
    body = JSON.stringify(payload);
  }
  return request(`${BASE}${pathname}`, { method, headers, body });
}

async function navigate(page, url) {
  for (let attempt = 0; ; attempt++) {
    try { return await page.goto(url); }
    catch (error) {
      if (attempt >= 2 || !/ERR_SOCKET_NOT_CONNECTED|ERR_CONNECTION_RESET/.test(String(error))) throw error;
      await sleep(300);
    }
  }
}
async function loginUi(page, email, password, secret, account) {
  await navigate(page, `${BASE}/entrar`);
  await page.getByLabel("E-mail").fill(email);
  await page.getByLabel("Senha").fill(password);
  await page.getByRole("button", { name: "Entrar" }).click();
  if (secret) {
    await page.getByLabel("Código do aplicativo ou de recuperação").fill(await freshCode(account, secret));
    await page.getByRole("button", { name: "Entrar" }).click();
  }
  await page.waitForURL(/\/(painel|mfa)$/);
}
async function enrollMfa(page, account) {
  await page.waitForURL(/\/mfa$/);
  await page.getByRole("button", { name: "Gerar códigos de recuperação" }).click();
  const secretText = await page.getByText(/^Segredo:/).textContent();
  const secret = secretText.replace("Segredo:", "").trim();
  assert.match(secret, /^[A-Z2-7]{16,}$/, "segredo do MFA em formato inesperado");
  await page.getByLabel("Código do aplicativo").fill(await freshCode(account, secret));
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.waitForURL(/\/painel$/);
  await page.getByRole("heading", { level: 1, name: "Painel" }).waitFor();
  return secret;
}


async function onboardAdmin(owner, tenantId, label) {
  const email = `${label}-${randomBytes(4).toString("hex")}@validacao.example`;
  const field = owner.page.locator(`#convite-admin-${tenantId}`);
  await field.waitFor();
  await field.fill(email);
  const inviteResponse = owner.page.waitForResponse((r) => r.url().includes(`/tenants/${tenantId}/admin-invitations`));
  await field.locator("xpath=ancestor::form").getByRole("button", { name: "Convidar administrador" }).click();
  const reply = await inviteResponse;
  assert.equal(reply.status(), 201, "convite recusado");
  const mail = await waitMail(email, "Convite");
  const { link, token } = linkAndToken(mail);
  assert.ok(!(await reply.text()).includes(token), "a resposta da API expôs o token do convite");
  const admin = await openContext();
  await admin.page.goto(link);
  const password = `Senha-${randomBytes(6).toString("hex")}`;
  await admin.page.getByLabel("Senha inicial").fill(password);
  await admin.page.getByRole("button", { name: "Criar acesso" }).click();
  const secret = await enrollMfa(admin.page, label);
  return { email, password, secret, token, link, admin };
}

// PNG 1x1 real: o servidor detecta o tipo pelos bytes.
const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==", "base64");
// PNG 1x1 com a cor dada: bytes distintos e válidos para o segundo anexo.
function makePng(r, g, b) {
  const chunk = (type, data) => {
    const body = Buffer.concat([Buffer.from(type), data]);
    const out = Buffer.alloc(12 + data.length);
    out.writeUInt32BE(data.length, 0);
    body.copy(out, 4);
    out.writeUInt32BE(zlib.crc32(body) >>> 0, 8 + data.length);
    return out;
  };
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(1, 0);
  ihdr.writeUInt32BE(1, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", ihdr), chunk("IDAT", zlib.deflateSync(Buffer.from([0, r, g, b]))), chunk("IEND", Buffer.alloc(0))]);
}
const sha = (bytes) => createHash("sha256").update(bytes).digest("hex");

let state = {};
try {
  state = JSON.parse(await readFile(STATE_FILE, "utf8"));
} catch {
  /* primeira fase */
}
for (const [account, step] of Object.entries(state.lastStep ?? {})) lastStep.set(account, step);
const save = () => writeFile(STATE_FILE, JSON.stringify(state), { mode: 0o600 });


async function signedInbound(text, from = "5511977776666") {
  const { appId, appSecret, waba, phoneId } = state.operation;
  const payload = JSON.stringify({
    object: "whatsapp_business_account",
    entry: [{ id: waba, changes: [{ field: "messages", value: { messaging_product: "whatsapp", metadata: { display_phone_number: `+${phoneId}`, phone_number_id: phoneId }, messages: [{ from, id: `wamid.${randomUUID()}`, timestamp: String(Math.floor(Date.now() / 1000)), text: { body: text }, type: "text" }] } }] }],
  });
  const signature = `sha256=${createHmac("sha256", appSecret).update(payload).digest("hex")}`;
  return request(`${BASE}/api/webhooks/meta?app_id=${appId}`, { method: "POST", headers: { "content-type": "application/json", "x-hub-signature-256": signature }, body: payload });
}

async function downloadAttachment(context, label, which = "operation") {
  const target = which === "keep" ? { inbox: state.operation.inbox, ...state.operation.keep } : state.operation;
  const { inbox, conversation, attachment, digest } = target;
  const res = await api(context, "POST", `/api/panel/inboxes/${inbox}/conversations/${conversation}/attachments/${attachment}/download`);
  assert.equal(res.status, 200, `${label}: URL assinada ${res.status} ${res.body.toString()}`);
  const { url, expires_in: ttl } = json(res);
  assert.ok(ttl > 0 && ttl <= 60, "URL assinada sem expiração curta");
  assert.equal(new URL(url).origin, STORAGE, "URL assinada fora do endpoint público do storage");
  const file = await request(url);
  assert.equal(file.status, 200, `${label}: leitura pelo storage ${file.status}`);
  assert.equal(sha(file.body), digest, `${label}: bytes diferem dos enviados`);
  assert.equal(file.headers["cache-control"], "private, no-store");
  const semAssinatura = await request(url.split("?")[0]);
  assert.notEqual(semAssinatura.status, 200, `${label}: objeto lido sem assinatura`);
  return url;
}

const results = [];
const check = (name) => {
  results.push(name);
  log(`ok: ${name}`);
};

try {
  if (PHASE === "before") {
    // 1. Primeiro acesso: bootstrap → login → MFA → painel.
    const owner = await openContext();
    await loginUi(owner.page, ADMIN_EMAIL, ADMIN_PASSWORD, null, "owner");
    state.ownerSecret = await enrollMfa(owner.page, "owner");
    const session = json(await api(owner.context, "GET", "/api/panel/session"));
    assert.equal(session.email, ADMIN_EMAIL);
    const cookies = await owner.context.cookies(BASE);
    const sessionCookie = cookies.find((cookie) => cookie.name === "__Host-session");
    const csrfCookie = cookies.find((cookie) => cookie.name === "__Host-csrf");
    assert.ok(sessionCookie?.secure && sessionCookie.httpOnly && sessionCookie.sameSite === "Lax" && sessionCookie.path === "/", "cookie de sessão sem proteção esperada");
    assert.ok(csrfCookie?.secure && !csrfCookie.httpOnly && csrfCookie.sameSite === "Lax" && csrfCookie.path === "/", "cookie CSRF sem proteção esperada");
    state.tenantA = session.tenant_id;
    check("primeiro acesso: login do bootstrap, cadastro do MFA e painel autenticado");

    // 2. Provedor novo e convite do primeiro administrador, pela interface; aceite pelo link e MFA.
    await owner.page.goto(`${BASE}/configuracoes/provedores`);
    const created = owner.page.waitForResponse((r) => r.url().endsWith("/api/platform/tenants") && r.request().method() === "POST");
    await owner.page.getByLabel("Limite de armazenamento (bytes)").fill("10485760");
    await owner.page.getByRole("button", { name: "Criar provedor" }).click();
    const tenantB = (await (await created).json()).id;
    state.tenantB = tenantB;
    const b = await onboardAdmin(owner, tenantB, "adminb");
    const sessionB = json(await api(b.admin.context, "GET", "/api/panel/session"));
    assert.equal(sessionB.email, b.email);
    assert.equal(sessionB.role, "admin");
    assert.equal(sessionB.tenant_id, tenantB, "admin B entrou em outro provedor");
    assert.notEqual(sessionB.tenant_id, session.tenant_id);
    const reuse = await request(`${BASE}/api/panel/invitations/accept`, {
      method: "POST",
      headers: { "content-type": "application/json", origin: BASE },
      body: JSON.stringify({ token: b.token, password: "Outra-senha-123456" }),
    });
    assert.ok(reuse.status >= 400 && reuse.status < 500, `token de convite reutilizável (${reuse.status})`);
    check("convite do admin de outro provedor: e-mail com link da origem do painel, token fora da resposta da API, aceite pelo link, MFA, token de uso único");

    // 3. Recuperação de senha pelo e-mail.
    const reset = await openContext();
    await reset.page.goto(`${BASE}/recuperar-senha`);
    await reset.page.getByLabel("E-mail").fill(b.email);
    await reset.page.getByRole("button", { name: "Enviar pedido" }).click();
    await reset.page.getByText("Se o e-mail existir, enviaremos as instruções.").waitFor();
    const resetData = linkAndToken(await waitMail(b.email, "Recuperação"));
    await reset.page.goto(resetData.link);
    b.password = `Nova-${randomBytes(6).toString("hex")}`;
    await reset.page.getByLabel("Nova senha").fill(b.password);
    await reset.page.getByRole("button", { name: "Guardar senha" }).click();
    await reset.page.getByText(/senha/i).first().waitFor();
    const opB = await openContext();
    await loginUi(opB.page, b.email, b.password, b.secret, "adminb");
    await opB.page.waitForURL(/\/painel$/);
    state.adminB = { email: b.email, password: b.password, secret: b.secret };
    check("recuperação de senha: e-mail com link, nova senha, login com MFA");

    // 4. Falha de entrega: o SMTP recusa; a API responde erro genérico, sem token, sem mensagem guardada.
    await request(`${SINK}/mode?reject=1`, { method: "POST" });
    const before = (await sinkMessages()).length;
    const refused = await api(owner.context, "POST", `/api/platform/tenants/${tenantB}/admin-invitations`, { email: `falha-${randomBytes(4).toString("hex")}@validacao.example` });
    assert.equal(refused.status, 503, `falha de entrega respondeu ${refused.status}`);
    assert.ok(!/token|Abra:/i.test(refused.body.toString()), "a resposta de falha expôs o token");
    assert.equal((await sinkMessages()).length, before, "mensagem recusada foi guardada");
    const resetRefused = await request(`${BASE}/api/panel/password-resets`, {
      method: "POST", headers: { origin: BASE, "content-type": "application/json" }, body: JSON.stringify({ email: b.email }),
    });
    assert.equal(resetRefused.status, 503, "recuperação não sinalizou falha de SMTP");
    assert.ok(!/token|Abra:/i.test(resetRefused.body.toString()), "falha de recuperação expôs token");
    await request(`${SINK}/mode?reject=0`, { method: "POST" });
    check("falha de entrega do SMTP: 503 sem token, nada entregue; entrega volta ao normal");

    // 5. Administrador do provedor do bootstrap (o superadmin não opera atendimento): adversário do isolamento.
    await owner.page.goto(`${BASE}/configuracoes/provedores`);
    const a = await onboardAdmin(owner, state.tenantA, "admina");
    state.adminA = { email: a.email, password: a.password, secret: a.secret };

    // 6. Atendimento no provedor B: canal Meta, conversa pelo worker, tempo real.
    const appId = `app-${randomBytes(4).toString("hex")}`;
    const appSecret = randomBytes(16).toString("hex");
    const phoneId = String(5511900000000 + Math.floor(Math.random() * 99999));
    const waba = `waba-${randomBytes(4).toString("hex")}`;
    const box = await api(opB.context, "POST", "/api/panel/channels/meta", {
      access_token: `token-${randomBytes(8).toString("hex")}`,
      app_id: appId,
      app_secret: appSecret,
      display_number: `+${phoneId}`,
      inbox_name: "Validação Compose",
      phone_number_id: phoneId,
      waba_id: waba,
    });
    assert.equal(box.status, 200, `canal: ${box.status} ${box.body.toString()}`);
    const inbox = json(box).inbox_id;
    // Configuração explícita da fila de entrada: sem setor padrão o worker recusa distribuir.
    const sector = await api(opB.context, "POST", "/api/panel/sectors", { name: "Atendimento" });
    assert.equal(sector.status, 201, `criar setor: ${sector.body.toString()}`);
    const sectorId = json(sector).id;
    for (const binding of [`/inboxes/${inbox}`, `/memberships/${sessionB.membership_id}`, `/inboxes/${inbox}/default`]) {
      const linked = await api(opB.context, "PUT", `/api/panel/sectors/${sectorId}${binding}`);
      assert.equal(linked.status, 200, `vincular setor ${binding}: ${linked.body.toString()}`);
    }
    const health = json(await api(opB.context, "GET", "/api/panel/channels/meta/health"));
    assert.equal(health.dispatch, "disabled", "a instalação deveria informar envio Meta desabilitado");
    // Mesma sessão válida: recusa sem CSRF e com Origin estrangeiro.
    const { cookie, csrf } = await cookieHeader(opB.context);
    for (const headers of [
      { cookie, origin: BASE },
      { cookie, origin: "https://foreign.invalid", "x-csrf-token": csrf },
    ]) {
      const denied = await request(`${BASE}/api/panel/channels/meta`, {
        method: "POST", headers: { ...headers, "content-type": "application/json" }, body: "{}",
      });
      assert.equal(denied.status, 403, "CSRF/Origin inválido aceito");
    }
    const deniedSocket = await request(`${BASE}/api/panel/realtime`, {
      headers: { cookie, origin: "https://foreign.invalid", upgrade: "websocket", connection: "Upgrade", "sec-websocket-version": "13", "sec-websocket-key": randomBytes(16).toString("base64") },
    });
    assert.equal(deniedSocket.status, 403, "WebSocket aceitou Origin estrangeiro");
    check("CSRF ausente e Origin estrangeiro recusados com sessão válida (HTTP e WebSocket)");
    const inbound = async (id, text, from = "5511977776666") => {
      const payload = JSON.stringify({
        object: "whatsapp_business_account",
        entry: [{ id: waba, changes: [{ field: "messages", value: { messaging_product: "whatsapp", metadata: { display_phone_number: `+${phoneId}`, phone_number_id: phoneId }, messages: [{ from, id, timestamp: String(Math.floor(Date.now() / 1000)), text: { body: text }, type: "text" }] } }] }],
      });
      const signature = `sha256=${createHmac("sha256", appSecret).update(payload).digest("hex")}`;
      return request(`${BASE}/api/webhooks/meta?app_id=${appId}`, { method: "POST", headers: { "content-type": "application/json", "x-hub-signature-256": signature }, body: payload });
    };
    const conversations = async () => {
      const snap = await api(opB.context, "GET", `/api/panel/inboxes/${inbox}/conversations`);
      return snap.status === 200 ? (json(snap).conversations ?? []) : [];
    };
    assert.equal((await inbound(`wamid.${randomUUID()}`, "Primeira mensagem do cliente")).status, 200);
    let conversation;
    for (let i = 0; i < 60 && !conversation; i++) {
      conversation = (await conversations())[0];
      if (!conversation) await sleep(500);
    }
    assert.ok(conversation, "o webhook assinado não virou conversa (worker)");

    await opB.page.goto(`${BASE}/conversas`);
    await opB.page.getByRole("combobox", { name: "Caixa", exact: true }).selectOption(inbox);
    await opB.page.getByRole("tab", { name: "Todas", exact: true }).click();
    await opB.page.getByRole("list", { name: "Conversas encontradas" }).getByRole("button").first().click();
    await opB.page.getByText("Primeira mensagem do cliente", { exact: true }).waitFor();
    const live = `Mensagem em tempo real ${randomBytes(3).toString("hex")}`;
    for (let i = 0; i < 60 && !opB.sockets.some(({ frames }) => frames.some((frame) => JSON.parse(frame).type === "snapshot")); i++) await sleep(100);
    assert.ok(opB.sockets.some(({ frames }) => frames.some((frame) => JSON.parse(frame).type === "snapshot")), "snapshot WebSocket não chegou");
    const liveExternal = `wamid.${randomUUID()}`;
    assert.equal((await inbound(liveExternal, live)).status, 200);
    await opB.page.getByText(live).first().waitFor({ timeout: 30_000 });
    assert.ok(opB.sockets.some(({ frames }) => frames.some((frame) => frame.includes(liveExternal))), "mensagem não foi observada nos frames WebSocket");
    check("atendimento: webhook assinado vira conversa pelo worker e a nova mensagem chega ao painel por WebSocket, sem recarregar");

    // 7. Anexos no storage real (Garage por TLS): um que será excluído depois (titular 1) e um mantido (titular 2).
    const attach = async (conv, bytes, name) => {
      const assume = await api(opB.context, "POST", `/api/panel/inboxes/${inbox}/conversations/${conv.id}/assume`, { expected_version: conv.version });
      assert.equal(assume.status, 200, `assumir: ${assume.status} ${assume.body.toString()}`);
      const base = `/api/panel/inboxes/${inbox}/conversations/${conv.id}/attachments`;
      const reg = await api(opB.context, "POST", base, { bytes: bytes.length, name, request_id: randomUUID() });
      assert.equal(reg.status, 200, `registro do anexo: ${reg.status} ${reg.body.toString()}`);
      const id = json(reg).id;
      const put = await api(opB.context, "PUT", `${base}/${id}/content`, bytes, { raw: true });
      assert.ok(put.status < 300, `envio dos bytes: ${put.status} ${put.body.toString()}`);
      const validated = await api(opB.context, "POST", `${base}/${id}/validate`);
      assert.equal(validated.status, 200, `validação: ${validated.status} ${validated.body.toString()}`);
      assert.equal(json(validated).state, "validated");
      return { id, version: json(assume).version, base };
    };
    conversation = (await conversations()).find((current) => current.id === conversation.id);
    const first = await attach(conversation, PNG, "validacao.png");
    state.operation = { inbox, conversation: conversation.id, attachment: first.id, digest: sha(PNG), version: first.version, contact: conversation.contact_id, protocol: conversation.protocol, appId, appSecret, waba, phoneId };
    await save();
    await downloadAttachment(opB.context, "anexo");
    const anonymous = await openContext();
    const noSession = await api(anonymous.context, "POST", `${first.base}/${first.id}/download`);
    assert.ok([401, 403, 404].includes(noSession.status), `sem sessão, o download respondeu ${noSession.status}`);
    const cross = await api(a.admin.context, "POST", `${first.base}/${first.id}/download`);
    assert.ok([403, 404].includes(cross.status), `administrador de outro provedor obteve acesso ao anexo (${cross.status})`);
    check("anexo: gravado e lido no Garage por TLS, URL assinada de 60 s, bytes idênticos, sem acesso sem assinatura, sem sessão e de outro provedor");

    assert.equal((await inbound(`wamid.${randomUUID()}`, "Segundo titular", "5511966665555")).status, 200);
    let second;
    for (let i = 0; i < 60 && !second; i++) {
      second = (await conversations()).find((c) => c.id !== conversation.id);
      if (!second) await sleep(500);
    }
    assert.ok(second, "a segunda conversa não foi criada");
    const keepPng = makePng(10, 200, 90);
    const kept = await attach(second, keepPng, "mantido.png");
    state.operation.keep = { conversation: second.id, attachment: kept.id, digest: sha(keepPng), contact: second.contact_id };
    await save();
    await downloadAttachment(opB.context, "anexo mantido", "keep");

    // 8. Envio com anexo: a Meta está desabilitada, então falha de forma explícita (conferida no banco pelo script do shell).
    const send = await api(opB.context, "POST", `/api/panel/inboxes/${inbox}/conversations/${conversation.id}/messages`, { attachment_id: first.id, body: "Resposta com anexo", client_request_id: randomUUID(), expected_version: first.version });
    assert.ok([200, 201].includes(send.status), `envio com anexo: ${send.status} ${send.body.toString()}`);
    check("envio com anexo aceito na fila (despacho Meta desabilitado será conferido no banco)");

    // 9. Métricas não saem pelo acesso público.
    for (const host of [BASE, STORAGE]) {
      const metrics = await request(`${host}/metrics`);
      assert.notEqual(metrics.status, 200, `${host}/metrics público`);
      assert.ok(!metrics.body.toString().includes("chatisp_"), "métricas vazaram");
    }
    check("/metrics não é servido pelo acesso público");
    assert.deepEqual([owner, b.admin, reset, opB, a.admin].flatMap((c) => c.problems), [], "erros de página ou 5xx durante a jornada");
  } else if (PHASE === "suspension") {
    const owner = await openContext();
    await loginUi(owner.page, ADMIN_EMAIL, ADMIN_PASSWORD, state.ownerSecret, "owner");
    const op = await openContext();
    await loginUi(op.page, state.adminB.email, state.adminB.password, state.adminB.secret, "adminb");
    const suspended = await api(owner.context, "POST", `/api/platform/tenants/${state.tenantB}/suspend`);
    assert.equal(suspended.status, 200, "suspensão recusada");
    const denied = await api(op.context, "GET", `/api/panel/inboxes/${state.operation.inbox}/conversations`);
    assert.ok([401, 403].includes(denied.status), "sessão operacional sobreviveu à suspensão");
    assert.equal((await signedInbound("Mensagem retida durante suspensão")).status, 200, "entrada transitória não persistiu");
    assert.equal((await signedInbound("Excesso sintético do limite de suspensão")).status, 500, "limite esgotado foi confirmado sem persistir");
    const active = await api(owner.context, "POST", `/api/platform/tenants/${state.tenantB}/reactivate`);
    assert.equal(active.status, 200, "reativação recusada");
    check("suspensão bloqueia operação, retém dentro do limite, recusa excesso sem ACK e permite reativação explícita");
  } else if (PHASE === "storage-down") {
    // Garage parado pelo shell: a falha precisa ser clara e o painel continua vivo.
    const op = await openContext();
    await loginUi(op.page, state.adminB.email, state.adminB.password, state.adminB.secret, "adminb");
    const { inbox, conversation } = state.operation;
    const down = await api(op.context, "POST", `/api/panel/inboxes/${inbox}/conversations/${conversation}/attachments/${state.operation.attachment}/download`);
    // A assinatura é local; a leitura é que falha.
    const url = down.status === 200 ? json(down).url : "";
    if (url) {
      const file = await request(url).catch((error) => ({ status: 0, error }));
      assert.notEqual(file.status, 200, "storage parado respondeu 200");
    }
    const reg = await api(op.context, "POST", `/api/panel/inboxes/${inbox}/conversations/${conversation}/attachments`, { bytes: PNG.length, name: "durante-queda.png", request_id: randomUUID() });
    let failed = reg.status >= 500;
    if (reg.status === 200) {
      const put = await api(op.context, "PUT", `/api/panel/inboxes/${inbox}/conversations/${conversation}/attachments/${json(reg).id}/content`, PNG, { raw: true });
      failed = put.status >= 500;
      assert.ok(!/AccessKey|SecretKey|secret/i.test(put.body.toString()), "a falha do storage expôs credencial");
    }
    assert.ok(failed, "gravar anexo com o storage parado não falhou");
    const ready = await request(`${BASE}/readyz`);
    assert.equal(ready.status, 200, "o painel deixou de responder com o storage parado");
    const page = await op.page.goto(`${BASE}/painel`);
    assert.ok(page.ok(), "o painel não carregou com o storage parado");
    check("storage indisponível: gravação falha com erro 5xx sem credencial, painel e /readyz seguem no ar");
  } else if (PHASE === "storage-up") {
    const op = await openContext();
    await loginUi(op.page, state.adminB.email, state.adminB.password, state.adminB.secret, "adminb");
    await downloadAttachment(op.context, "anexo depois da volta do storage");
    const { inbox, conversation } = state.operation;
    const base = `/api/panel/inboxes/${inbox}/conversations/${conversation}/attachments`;
    const reg = await api(op.context, "POST", base, { bytes: PNG.length, name: "depois-da-volta.png", request_id: randomUUID() });
    assert.equal(reg.status, 200);
    const put = await api(op.context, "PUT", `${base}/${json(reg).id}/content`, PNG, { raw: true });
    assert.ok(put.status < 300, `gravação após a volta: ${put.status}`);
    check("storage de volta: leitura e nova gravação funcionam");
  } else if (PHASE === "after") {
    const owner = await openContext();
    await loginUi(owner.page, ADMIN_EMAIL, ADMIN_PASSWORD, state.ownerSecret, "owner");
    await owner.page.getByRole("heading", { level: 1, name: "Painel" }).waitFor();
    const op = await openContext();
    await loginUi(op.page, state.adminB.email, state.adminB.password, state.adminB.secret, "adminb");
    check("depois do reinício: superadmin e administrador do provedor entram com a mesma senha e o mesmo MFA");
    await downloadAttachment(op.context, "anexo depois do reinício");
    await downloadAttachment(op.context, "anexo mantido depois do reinício", "keep");
    const { inbox } = state.operation;
    const snap = json(await api(op.context, "GET", `/api/panel/inboxes/${inbox}/conversations?conversation_id=${state.operation.conversation}`));
    assert.ok((snap.messages ?? []).some((m) => /tempo real/.test(m.body ?? m.text ?? "")), "mensagens não persistiram após o reinício");
    check("depois do reinício: anexos com os mesmos bytes e mensagens persistidos");
    await op.page.goto(`${BASE}/conversas`);
    await op.page.getByRole("combobox", { name: "Caixa", exact: true }).selectOption(inbox);
    await op.page.getByRole("tab", { name: "Todas", exact: true }).click();
    await op.page.getByRole("list", { name: "Conversas encontradas" }).getByRole("button").filter({ hasText: state.operation.protocol ?? "Protocolo" }).first().click();
    const live = `Depois do reinício ${randomBytes(3).toString("hex")}`;
    assert.equal((await signedInbound(live)).status, 200);
    await op.page.getByText(live).first().waitFor({ timeout: 30_000 });
    check("depois do reinício: webhook, worker e WebSocket voltaram a funcionar");
  } else if (PHASE === "exclude") {
    // Exclusão do titular 1 (decisão de privacidade) depois do backup: apaga o anexo e registra o livro.
    const op = await openContext();
    await loginUi(op.page, state.adminB.email, state.adminB.password, state.adminB.secret, "adminb");
    const del = await api(op.context, "POST", `/api/panel/contacts/${state.operation.contact}/privacy-deletion`, { confirm: "excluir titular" });
    assert.equal(del.status, 200, `exclusão do titular: ${del.status} ${del.body.toString()}`);
    const gone = await api(op.context, "POST", `/api/panel/inboxes/${state.operation.inbox}/conversations/${state.operation.conversation}/attachments/${state.operation.attachment}/download`);
    assert.ok([404, 409].includes(gone.status), `anexo excluído ainda responde ${gone.status}`);
    await downloadAttachment(op.context, "anexo do outro titular continua", "keep");
    check("exclusão do titular 1: anexo some, o do titular 2 permanece");
  } else if (PHASE === "restored") {
    // Depois da restauração de desastre: senhas e MFA vêm do banco restaurado (o MFA só abre com as chaves recuperadas).
    const owner = await openContext();
    await loginUi(owner.page, ADMIN_EMAIL, ADMIN_PASSWORD, state.ownerSecret, "owner");
    const op = await openContext();
    await loginUi(op.page, state.adminB.email, state.adminB.password, state.adminB.secret, "adminb");
    const a = await openContext();
    await loginUi(a.page, state.adminA.email, state.adminA.password, state.adminA.secret, "admina");
    check("restauração: os três administradores entram com senha e MFA (cifrado com as chaves recuperadas)");
    await downloadAttachment(op.context, "anexo mantido restaurado", "keep");
    const { inbox, conversation, attachment } = state.operation;
    const gone = await api(op.context, "POST", `/api/panel/inboxes/${inbox}/conversations/${conversation}/attachments/${attachment}/download`);
    assert.ok([404, 409].includes(gone.status), `anexo excluído voltou (${gone.status})`);
    const snap = json(await api(op.context, "GET", `/api/panel/inboxes/${inbox}/conversations`));
    assert.ok((snap.conversations ?? []).length >= 2, "conversas não voltaram");
    check("restauração: anexo mantido volta com os mesmos bytes; anexo excluído continua excluído; conversas presentes");
    assert.equal((await signedInbound(`Depois da restauração ${randomBytes(3).toString("hex")}`)).status, 200);
    check("restauração: o webhook assinado é aceito, então o segredo do app abriu com a chave recuperada");
  } else {
    throw new Error(`fase desconhecida: ${PHASE}`);
  }
  await save();
  console.log(`JORNADA ${PHASE} OK (${results.length} verificações)`);
} finally {
  for (const context of contexts) await context.close().catch(() => {});
  await browser.close();
  await mkdir(path.dirname(STATE_FILE), { recursive: true }).catch(() => {});
}
