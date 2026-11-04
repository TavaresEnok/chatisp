import { expect as playwrightExpect, type Browser, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { createHmac, randomUUID } from "node:crypto";

const expect = playwrightExpect.configure({ timeout: 15_000 });

type Actor = { Token: string; CSRF: string; User: string; Member: string; Role: string };
type Fixture = {
  App: string; Secret: string; Waba: string; Phone: string;
  M3B: { Tenant: string; Inbox: string; Source: string; Destination: string; Actors: Actor[] };
  M4A: {
    ControlURL: string; ControlToken: string; Timezone: string;
    DriverTemplateID: string; FakeTemplateID: string;
    Admin: Actor; Outsider: Actor;
    Simulation: string;
  };
};
type History = {
  head: { id: string; version: number; contact_id: string; protocol: string; assignee_id: string };
  messages: { body: string; direction: string; local_state: string }[];
};

async function api<T>(page: Page, path: string, csrf: string, method = "GET", body?: unknown): Promise<{ status: number; body: T }> {
  return page.evaluate(async ({ path, csrf, method, body }) => {
    const response = await fetch(path, {
      method,
      headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    });
    const text = await response.text();
    return { status: response.status, body: text ? JSON.parse(text) as T : {} as T };
  }, { path, csrf, method, body });
}

async function openPanel(browser: Browser, actor: Actor) {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  await context.addCookies([{ name: "__Host-session", value: actor.Token, url: "https://localhost:5174", secure: true, httpOnly: true, sameSite: "Strict" }]);
  // Same socket bookkeeping as the reviewed M3B runner: headless Chromium keeps
  // the panel socket open, and closing it is the drop the panel already handles.
  await context.addInitScript(() => {
    const native = WebSocket;
    const open: WebSocket[] = [];
    function Socket(this: unknown, url: string | URL, protocols?: string | string[]) {
      const socket = protocols === undefined ? new native(url) : new native(url, protocols);
      open.push(socket);
      return socket;
    }
    Socket.prototype = native.prototype;
    Object.setPrototypeOf(Socket, native);
    Socket.CONNECTING = native.CONNECTING;
    Socket.OPEN = native.OPEN;
    Socket.CLOSING = native.CLOSING;
    Socket.CLOSED = native.CLOSED;
    window.WebSocket = Socket as unknown as typeof WebSocket;
    (window as unknown as { __chatispCloseSockets?: () => void }).__chatispCloseSockets = () => {
      for (const socket of open) if (socket.readyState < WebSocket.CLOSING) socket.close();
    };
  });
  const page = await context.newPage();
  page.setDefaultTimeout(20_000);
  page.setDefaultNavigationTimeout(60_000);
  const frames: string[] = [];
  const http: string[] = [];
  page.on("websocket", ws => ws.on("framereceived", frame => frames.push(String(frame.payload))));
  page.on("response", async response => {
    if (!response.url().includes("attendant-signature")) return;
    const body = await response.text().catch(() => "");
    http.push(`${response.status()} ${body.slice(0, 240)}`);
  });
  await page.goto("https://localhost:5174/entrar", { waitUntil: "domcontentloaded", timeout: 60_000 });
  await page.evaluate(async csrf => {
    const response = await fetch("/api/panel/session");
    if (!response.ok) throw new Error(`fixture session: ${response.status}`);
    const data = await response.json();
    const { rememberSession } = await import(/* @vite-ignore */ "/src/api/panel.ts");
    rememberSession({ ...data, csrf_token: csrf });
    window.history.pushState({}, "", "/conversas");
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, actor.CSRF);
  return { page, context, frames, http };
}

async function refetchPanel(page: Page, marker: string) {
  const probe = await page.evaluate(async (marker) => {
    const root = document.getElementById("root") ?? document.body;
    const fiberKey = Object.keys(root).find(key => key.startsWith("__reactContainer$") || key.startsWith("__reactFiber$"));
    type Fiber = { child?: Fiber; sibling?: Fiber; memoizedProps?: { value?: unknown; client?: unknown }; stateNode?: unknown; memoizedState?: { memoizedState?: unknown } | null };
    const seen = new Set<Fiber>();
    const queue: Fiber[] = [];
    if (fiberKey) queue.push(Reflect.get(root, fiberKey) as Fiber);
    let queryClient: { refetchQueries: (filters: { predicate: (query: { queryKey: readonly unknown[] }) => boolean }) => Promise<unknown> } | undefined;
    const take = (value: unknown) => {
      if (value && typeof value === "object" && "refetchQueries" in value && "getQueryCache" in value) queryClient = value as typeof queryClient;
    };
    while (queue.length > 0 && !queryClient) {
      const node = queue.shift();
      if (!node || seen.has(node)) continue;
      seen.add(node);
      take(node.memoizedProps?.value);
      take(node.memoizedProps?.client);
      take(node.stateNode);
      take(node.memoizedState?.memoizedState);
      if (node.child) queue.push(node.child);
      if (node.sibling) queue.push(node.sibling);
    }
    if (!queryClient) return false;
    await queryClient.refetchQueries({ predicate: query => query.queryKey.includes(marker) });
    return true;
  }, marker);
  if (!probe) throw new Error(`consulta ${marker} sem cliente do painel`);
}

async function loadSignature(page: Page) {
  const probe = await page.evaluate(async () => {
    const mod = await import(/* @vite-ignore */ "/src/api/cannedreplies.ts");
    try {
      return { ok: true as const, signature: await mod.getSignature() };
    } catch (error) {
      const failed = error as { name?: string; message?: string; status?: number };
      return { ok: false as const, name: failed.name ?? "", message: failed.message ?? "", status: failed.status ?? 0 };
    }
  });
  if (!probe.ok) throw new Error(`assinatura da API falhou: ${probe.status} ${probe.name} ${probe.message}`);
  await refetchPanel(page, "signature");
  return probe;
}

async function bindInbox(page: Page, inbox: string) {
  await page.getByRole("combobox", { name: "Caixa", exact: true }).selectOption(inbox);
  await page.getByRole("tab", { name: "Todas", exact: true }).click();
}

function composer(page: Page) { return page.getByRole("textbox", { name: "Mensagem", exact: true }); }
function rows(page: Page) { return page.getByRole("list", { name: "Conversas encontradas" }).getByRole("button"); }

function localDateTime(iso: string) {
  const date = new Date(iso);
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

async function receive(fixture: Fixture, from: string, text: string) {
  const body = JSON.stringify({
    object: "whatsapp_business_account",
    entry: [{
      id: fixture.Waba,
      changes: [{
        field: "messages",
        value: {
          messaging_product: "whatsapp",
          metadata: { phone_number_id: fixture.Phone, display_phone_number: fixture.Phone },
          contacts: [{ profile: { name: "Cliente Simulado" }, wa_id: from }],
          messages: [{ from, id: `wamid.simulation.${randomUUID()}`, timestamp: String(Math.floor(Date.now() / 1000)), type: "text", text: { body: text } }],
        },
      }],
    }],
  });
  const response = await fetch(`http://127.0.0.1:${process.env.CHATISP_E2E_API_PORT ?? "8080"}/api/webhooks/meta?app_id=${encodeURIComponent(fixture.App)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Hub-Signature-256": `sha256=${createHmac("sha256", fixture.Secret).update(body).digest("hex")}`, "User-Agent": "chatisp-metacloud-simulation" },
    body,
  });
  expect(response.status).toBe(200);
}

export async function runM4A(browser: Browser, testInfo: { attach(name: string, options: { body: string; contentType: string }): Promise<void> }) {
  const filename = process.env.M4A_FIXTURE_FILE;
  if (!filename) throw new Error("Fixture ausente: execute npm run test:e2e:m4a; somente localhost. Sem skip.");
  const fixture: Fixture = JSON.parse(await readFile(filename, "utf8"));
  if (!fixture.M4A || !fixture.M3B || fixture.M3B.Actors.length !== 3) throw new Error("Fixture M4A inválida");
  const agentActor = fixture.M3B.Actors[0]!;
  const inbox = fixture.M3B.Inbox;
  const evidence: Record<string, string> = { simulation: fixture.M4A.Simulation, timezone: fixture.M4A.Timezone };
  const admin = await openPanel(browser, fixture.M4A.Admin);
  const agent = await openPanel(browser, agentActor);
  let outsider: Awaited<ReturnType<typeof openPanel>> | undefined;
  try {
    await bindInbox(admin.page, inbox);
    await bindInbox(agent.page, inbox);
    await expect.poll(() => agent.frames.some(frame => { try { return JSON.parse(frame).type === "snapshot"; } catch { return false; } })).toBe(true);
    await receive(fixture, "5511988887001", "olá janela M4A");
    await expect(rows(agent.page)).toHaveCount(1);
    const label = await rows(agent.page).first().innerText();
    const protocol = label.match(/Protocolo\s+(\S+)/)?.[1];
    expect(protocol).toBeTruthy();
    await rows(agent.page).first().click();
    await expect(composer(agent.page)).toBeVisible();
    await expect(agent.page.getByText("Janela oficial de 24 horas: indisponível neste painel")).toBeVisible();
    const session = await api<{ email: string }>(agent.page, "/api/panel/session", agentActor.CSRF);
    expect(session.status).toBe(200);
    const email = session.body.email;
    const snapshot = await api<{ conversations: { id: string; protocol: string }[] }>(agent.page, `/api/panel/inboxes/${inbox}/conversations`, agentActor.CSRF);
    const id = snapshot.body.conversations.find(item => item.protocol === protocol)!.id;
    const item = `/api/panel/inboxes/${inbox}/conversations/${id}`;
    const history = () => api<History>(agent.page, `${item}/history`, agentActor.CSRF);
    await agent.page.getByRole("button", { name: "Assumir", exact: true }).click();
    await expect.poll(async () => (await history()).body.head.assignee_id).toBe(agentActor.Member);

    await admin.page.getByRole("link", { name: "Configurações", exact: true }).click();
    await admin.page.getByRole("button", { name: "Mostrar respostas compartilhadas" }).click();
    const created = admin.page.getByRole("form", { name: "Nova resposta" });
    await created.getByLabel("Atalho").fill("reiniciar");
    await created.getByLabel("Título").fill("Reiniciar modem");
    await created.getByLabel("Conteúdo").fill("Olá {{nome_cliente}}, protocolo {{protocolo}}.");
    await created.getByRole("button", { name: "Criar resposta" }).click();
    await expect(admin.page.getByText("/reiniciar — Reiniciar modem")).toBeVisible();
    const second = admin.page.getByRole("form", { name: "Nova resposta" });
    await second.getByLabel("Atalho").fill("descartar");
    await second.getByLabel("Título").fill("Descartar");
    await second.getByLabel("Conteúdo").fill("Texto {{protocolo}} para exclusão.");
    await second.getByRole("button", { name: "Criar resposta" }).click();
    const discard = admin.page.locator("ul").getByRole("article").filter({ hasText: "/descartar — Descartar" });
    await expect(discard).toHaveCount(1);
    await discard.getByRole("button", { name: "Excluir resposta" }).click();
    await expect(discard).toHaveCount(0);
    const kept = admin.page.locator("ul").getByRole("article").filter({ hasText: "/reiniciar — Reiniciar modem" });
    await kept.getByRole("button", { name: "Editar resposta" }).click();
    const editor = admin.page.getByRole("form", { name: "Editar resposta" });
    await editor.getByLabel("Título").fill("Reiniciar atendimento");
    await editor.getByRole("button", { name: "Atualizar resposta" }).click();
    await expect(admin.page.getByText("/reiniciar — Reiniciar atendimento")).toBeVisible();
    await admin.page.getByRole("link", { name: "Conversas", exact: true }).click();
    await admin.page.getByRole("link", { name: "Configurações", exact: true }).click();
    await admin.page.getByRole("button", { name: "Mostrar respostas compartilhadas" }).click();
    const signature = admin.page.getByRole("region", { name: "Assinatura do atendente" });
    await loadSignature(admin.page);
    await expect(signature.getByRole("checkbox", { name: "Aplicar prefixo na aceitação" }), admin.http.join(" | ") || "sem HTTP de assinatura").toBeVisible();
    await signature.getByRole("checkbox", { name: "Aplicar prefixo na aceitação" }).check();
    await signature.getByRole("button", { name: "Gravar assinatura" }).click();
    // O formulário remonta com a versão nova e apaga o aviso transitório.
    await expect(signature.getByText("Versão 1.")).toBeVisible();
    await expect(signature.getByRole("checkbox", { name: "Aplicar prefixo na aceitação" })).toBeChecked();
    const stored = await api<{ enabled: boolean; prefix: string; version: number }>(admin.page, "/api/panel/attendant-signature", fixture.M4A.Admin.CSRF);
    expect(stored.status).toBe(200);
    expect(stored.body.enabled).toBe(true);
    expect(stored.body.prefix).toBe("{{nome_atendente}}: ");
    expect(stored.body.version).toBe(1);
    evidence.canned = "crud, slash e assinatura no painel";

    await admin.page.getByRole("button", { name: "Mostrar horário e plantão" }).click();
    await refetchPanel(admin.page, "inboxes");
    await refetchPanel(admin.page, "sectors");
    await admin.page.getByRole("combobox", { name: "Caixa do horário", exact: true }).selectOption({ label: "SIMULAÇÃO Atendimento" });
    await refetchPanel(admin.page, "configuration");
    await expect(admin.page.getByText("Fuso do provedor: America/Sao_Paulo")).toBeVisible();
    await admin.page.getByRole("button", { name: "Adicionar intervalo" }).click();
    await admin.page.getByLabel("Boas-vindas").fill("Bem-vindo simulação");
    await admin.page.getByLabel("Fora do expediente").fill("Fora do expediente simulação");
    await admin.page.getByLabel("Ninguém disponível").fill("Ninguém disponível simulação");
    await admin.page.getByLabel("Plantão").selectOption({ label: "SIMULAÇÃO Destino M3B" });
    const hoursPath = `/api/panel/inboxes/${inbox}/business-hours`;
    const isHoursConfig = (response: { url(): string; request(): { method(): string } }) => {
      const url = response.url();
      return url.includes(hoursPath) && !url.includes("/evaluation");
    };
    const putDone = admin.page.waitForResponse((response) =>
      response.request().method() === "PUT" && isHoursConfig(response) && response.ok());
    await admin.page.getByRole("button", { name: "Substituir horário" }).click();
    const savedHours = await (await putDone).json() as { version: number; configured: boolean };
    expect(savedHours.configured).toBe(true);
    expect(savedHours.version).toBeGreaterThan(0);
    // O HoursForm remonta quando a query troca de versão e zera instante/decisão.
    // A consulta só começa depois do PUT, do refetch dessa versão e do GET da API.
    const refreshed = admin.page.waitForResponse(async (response) => {
      if (response.request().method() !== "GET" || !isHoursConfig(response) || !response.ok()) return false;
      const body = await response.json() as { version: number };
      return body.version === savedHours.version;
    });
    await refetchPanel(admin.page, "configuration");
    await refreshed;
    await expect(admin.page.getByText("Há configuração ativa.")).toBeVisible();
    const hours = await api<{ version: number; timezone: string; config: { welcome: string; outside_hours: string; nobody_available: string } }>(admin.page, hoursPath, fixture.M4A.Admin.CSRF);
    expect(hours.status).toBe(200);
    expect(hours.body.version).toBe(savedHours.version);
    expect(hours.body.timezone).toBe("America/Sao_Paulo");
    expect(hours.body.config.welcome).toBe("Bem-vindo simulação");
    expect(hours.body.config.outside_hours).toBe("Fora do expediente simulação");
    expect(hours.body.config.nobody_available).toBe("Ninguém disponível simulação");
    const decide = async (iso: string, label: string) => {
      await expect(async () => {
        await admin.page.getByLabel("Instante avaliado").fill(localDateTime(iso), { timeout: 3_000 });
        await admin.page.getByRole("button", { name: "Consultar decisão" }).click({ timeout: 3_000 });
        await expect(admin.page.getByText(label)).toBeVisible({ timeout: 3_000 });
      }).toPass({ timeout: 20_000 });
    };
    await decide("2026-03-02T13:00:00Z", "Dentro do expediente.");
    await decide("2026-03-01T13:00:00Z", "Fora do expediente.");
    evidence.hours = "configuração e decisão por caixa no fuso do tenant, após refetch da versão gravada; sem envio automático";

    await composer(agent.page).fill("/reiniciar");
    const option = agent.page.getByRole("listbox", { name: "Respostas compartilhadas" }).getByRole("option");
    await expect(option).toContainText("Reiniciar atendimento");
    await composer(agent.page).press("Enter");
    await expect(composer(agent.page)).toHaveValue(`Olá Cliente Simulado, protocolo ${protocol}.`);
    await agent.page.getByRole("button", { name: "Enviar", exact: true }).click();
    await expect.poll(async () => (await history()).body.messages.some(message => message.body === `${email}: Olá Cliente Simulado, protocolo ${protocol}.`)).toBe(true);
    evidence.signature = "prefixo do e-mail aplicado uma vez na aceitação";

    const consent = agent.page.getByRole("form", { name: "Consentimento do contato" });
    await consent.getByLabel("Finalidade do consentimento").fill("cobranca");
    await consent.getByLabel("Origem").fill("painel");
    await consent.getByLabel("Evidência").fill("evento:simulacao-m4a");
    await consent.getByRole("button", { name: "Registrar consentimento" }).click();
    await expect(consent.getByText("Concessão registrada.")).toBeVisible();
    const closed = await fetch(`${fixture.M4A.ControlURL}/simulation/close-window`, { method: "POST", headers: { "X-Simulation-Token": fixture.M4A.ControlToken } });
    expect(closed.status).toBe(200);
    const closedBody = await closed.json() as { aged_inbound: number; lease_released: boolean; meta_calls: number };
    expect(closedBody.aged_inbound).toBeGreaterThan(0);
    expect(closedBody.lease_released).toBe(false);
    expect(closedBody.meta_calls).toBe(0);
    const template = agent.page.locator("article").filter({ hasText: "reinicio_simulacao" });
    await template.getByLabel("Finalidade do template").fill("cobranca");
    await template.getByRole("button", { name: "Colocar template na fila" }).click();
    await expect(template.getByText("Template colocado na fila existente.")).toBeVisible();
    const queuedTemplate = await api<{ state?: string; detail?: string }>(agent.page, `${item}/template-messages`, agentActor.CSRF, "POST", {
      expected_version: (await history()).body.head.version,
      client_request_id: randomUUID(),
      template_id: fixture.M4A.DriverTemplateID,
      language: "pt_BR",
      purpose: "cobranca",
    });
    expect(queuedTemplate.status).toBe(201);
    expect(queuedTemplate.body.state).toBe("queued");
    evidence.template = "template driver autorizado entrou na fila com consentimento válido e janela já fechada";

    await admin.page.getByRole("link", { name: "Conversas", exact: true }).click();
    await bindInbox(admin.page, inbox);
    await rows(admin.page).filter({ hasText: protocol! }).click();
    await admin.page.getByRole("button", { name: "Sincronizar catálogo" }).click();
    await expect(admin.page.getByText("não está aprovado por driver")).toBeVisible();
    const rejected = await api<{ detail?: string; title?: string }>(agent.page, `${item}/template-messages`, agentActor.CSRF, "POST", {
      expected_version: (await history()).body.head.version,
      client_request_id: randomUUID(),
      template_id: fixture.M4A.FakeTemplateID,
      language: "pt_BR",
      purpose: "cobranca",
    });
    expect(rejected.status).toBe(422);
    expect(rejected.body.detail).toBe("política de canal recusou o envio: template_not_authorized");
    evidence.template = "consentimento válido e janela fechada: driver 201; fake 422 template_not_authorized";

    await consent.getByRole("checkbox", { name: "Revogar" }).check();
    await consent.getByRole("button", { name: "Registrar consentimento" }).click();
    await expect(consent.getByText(/Revogação registrada/)).toBeVisible();
    await expect.poll(async () => {
      const queued = (await history()).body.messages.filter(message => message.body === "sim-driver-reinicio");
      return queued.length >= 2 && queued.every(message => message.local_state === "cancelled");
    }).toBe(true);
    expect((await history()).body.messages.find(message => message.body.includes("Olá Cliente Simulado"))?.local_state).toBe("queued");
    const optout = await api<{ detail?: string }>(agent.page, `${item}/template-messages`, agentActor.CSRF, "POST", {
      expected_version: (await history()).body.head.version,
      client_request_id: randomUUID(),
      template_id: fixture.M4A.FakeTemplateID,
      language: "pt_BR",
      purpose: "cobranca",
    });
    expect(optout.status).toBe(422);
    expect(optout.body.detail).toBe("política de canal recusou o envio: consent_required");
    evidence.optout = "revogação cancelou o template proativo com a fila ainda de pé; nova tentativa 422 consent_required";

    const file = agent.page.getByLabel("Arquivo", { exact: true });
    await file.setInputFiles({ name: "foto.jpg", mimeType: "image/jpeg", buffer: Buffer.from("<!DOCTYPE html><html><body>disfarce</body></html>") });
    await expect(agent.page.getByText("anexo inválido")).toBeVisible();
    await file.setInputFiles({ name: "nota.pdf", mimeType: "application/pdf", buffer: Buffer.from("%PDF-1.7\nsimulacao\n") });
    await expect(agent.page.getByRole("button", { name: "Obter download autorizado" })).toBeVisible();
    const downloadRequest = agent.page.waitForRequest(request => request.method() === "POST" && request.url().includes("/download"));
    await agent.page.getByRole("button", { name: "Obter download autorizado" }).click();
    const authorized = await downloadRequest;
    await expect(agent.page.getByRole("link", { name: "Baixar nota.pdf" })).toBeVisible();
    const signed = await agent.page.getByRole("link", { name: "Baixar nota.pdf" }).getAttribute("href");
    expect(signed).toBeTruthy();
    const fetched = await fetch(signed!, { credentials: "omit" });
    expect(fetched.status).toBe(200);
    expect(fetched.headers.get("x-chatisp-simulation")).toBe("local-attachment");
    expect(await fetched.text()).toContain("%PDF-1.7");
    outsider = await openPanel(browser, fixture.M4A.Outsider);
    const denied = await api(outsider.page, new URL(authorized.url()).pathname, fixture.M4A.Outsider.CSRF, "POST", {});
    expect(denied.status).toBe(403);
    evidence.attachment = "tipo disfarçado recusado; download autorizado no setor; outro setor 403; URL local simulada";

    const expired = await fetch(`${fixture.M4A.ControlURL}/simulation/expire-window`, { method: "POST", headers: { "X-Simulation-Token": fixture.M4A.ControlToken } });
    expect(expired.status).toBe(200);
    const clock = await expired.json() as { lease_released: boolean; aged_inbound: number; meta_calls: number };
    expect(clock.lease_released).toBe(true);
    expect(clock.aged_inbound).toBeGreaterThan(0);
    expect(clock.meta_calls).toBe(0);
    await expect.poll(async () => (await history()).body.messages.find(message => message.body.includes("Olá Cliente Simulado"))?.local_state, { timeout: 20_000 }).toBe("cancelled");
    const gone = await fetch(signed!, { credentials: "omit" });
    expect(gone.status).toBe(410);
    const status = await fetch(`${fixture.M4A.ControlURL}/simulation/status`, { headers: { "X-Simulation-Token": fixture.M4A.ControlToken } });
    const calls = await status.json() as { meta_calls: number };
    expect(calls.meta_calls).toBe(0);
    evidence.window = "âncora recuada 25h pela fixture; texto livre falhou sem chamada externa; URL assinada expirada";
    await testInfo.attach("m4a-jornada", { body: JSON.stringify(evidence), contentType: "application/json" });
  } finally {
    await admin.context.close();
    await agent.context.close();
    await outsider?.context.close();
  }
}
