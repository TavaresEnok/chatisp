import { expect as playwrightExpect, type Browser, type Page } from "@playwright/test";
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { createHmac, randomUUID } from "node:crypto";

const expect = playwrightExpect.configure({ timeout: 20_000 });

type Actor = { Token: string; CSRF: string; User: string; Member: string; Role: string };
type Fixture = {
  App: string; Secret: string; Waba: string; Phone: string;
  M3B: { Tenant: string; Inbox: string; Source: string; Destination: string; Actors: Actor[] };
  M4A: { Admin: Actor };
  M4B: { ImageMediaID: string; CustomerID: string; ContractID: string; InvoiceLabel: string; Simulation: string };
};
type HookHit = { type: string; signature: string; body: string };

async function openPanel(browser: Browser, actor: Actor) {
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  await context.addCookies([{ name: "__Host-session", value: actor.Token, url: "https://localhost:5174", secure: true, httpOnly: true, sameSite: "Strict" }]);
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
    // Simulated capture. The panel still uses MediaRecorder and sends the original blob.
    // 0.6 s synthetic tone, 8 kHz mono MP3 (~1 KB). The server now runs MPEG through the
    // limited ffmpeg, so a bare ID3 header is refused as undecodable audio.
    const mpeg = Uint8Array.from(atob("SUQzBAAAAAAAI1RTU0UAAAAPAAADTGF2ZjU4LjI5LjEwMAAAAAAAAAAAAAAA/+M4wAAAAAAAAAAAAEluZm8AAAAPAAAACwAAA/AASUlJSUlJSUlJW1tbW1tbW1tbbW1tbW1tbW1tgICAgICAgICAkpKSkpKSkpKSpKSkpKSkpKSktra2tra2tra2ycnJycnJycnJ29vb29vb29vb7e3t7e3t7e3t////////////AAAAAExhdmM1OC41NAAAAAAAAAAAAAAAACQDwAAAAAAAAAPwiozOGwAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/+MYxAANIJaceUwAAAgLdwL3ve973vSlKUpSnXve97/O007EMCYEwJiOf8OBAEMTh///nOn3f0ghy/uxAAwf4Y6VJoiwyxM//+MYxAYOcPKUAZRQAOOaK1HKELf4WeEcgd4wBjwknAO+SEaSHgdCKJzCSAyBRFYRQiv/yEej044iEv+EgaEp0Gn//////////+MYxAcN6KZAAd4AAP/////5UlsAgAk1hCAIYEIKBg1gtGDmI8Yc8ixjCCGGF6CYYGYGgOAjL7g4A1IV1Or///fVgRe0wIIy/+MYxAoM2KY0AAa8RCSNAlOPYMMAUs23aCzPQD/MJ0EIwMgCygApRNE5UrDrH////////q/9Vf//+AF5p6NHTwMuHOQ4MNMQ/+MYxBENMKY0AAa8RBN4R/00MQyzC0A6MEcBswHgEjAEAILPpeMDx////////3//rf//+Tt6l0DgEwITApsDIcEj0GEB0UYH/+MYxBcMeKY0AAb8RCMASA6hYC4AgHhgAZEAImyx2///////////1P//+IsySNLhGLDGaInDXGF0GObXrKpngBLmE2A4GA0h/+MYxCAMOKY4AAa8RMAuluocwFpWH//////////X///5Q4bDH7YAYZpzeGEKIOah0FplnhnmDkB0YFIC5gHAFFkEH2IOXir//+MYxCoKGKY8AAZ8RP/06jMPbDYxOgbOAYDoDC8FYDDgQoDJp9oDMWMwDEsEYDB4DoBQGgFACAFAQDVg4XrIMQQRiMWI3KYy/+MYxDwMYKZEAVYAAOzKv//jduMQ4/zWn1az//+NAcVSHHEEqmSSphAv//gjjrPsOUAsAEAMPIeQ8//9/9ZUO4dyJqauNvTc/+MYxEUYORqUMZpYAJhUFREFQV+5NyYiCoKiIKgr+CBc+GC85//l5xNMQU1FMy4xMDBVVVVVVVVVVVVVVVVVVVVVVVVVVVVV/+MYxB8AAANIAcAAAFVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVVV"), (char) => char.charCodeAt(0));
    class SimulatedRecorder {
      mimeType: string;
      state = "inactive";
      ondataavailable: ((event: { data: Blob }) => void) | null = null;
      onstop: (() => void) | null = null;
      constructor(_stream: unknown, options?: { mimeType?: string }) {
        this.mimeType = options?.mimeType || "audio/mpeg";
      }
      start() { this.state = "recording"; }
      stop() {
        this.state = "inactive";
        const blob = new Blob([mpeg], { type: this.mimeType || "audio/mpeg" });
        this.ondataavailable?.({ data: blob });
        this.onstop?.();
      }
      static isTypeSupported() { return false; }
    }
    window.MediaRecorder = SimulatedRecorder as unknown as typeof MediaRecorder;
    const devices = navigator.mediaDevices ?? (navigator.mediaDevices = {} as MediaDevices);
    devices.getUserMedia = async () => ({ getTracks: () => [{ stop() {} }] }) as unknown as MediaStream;
  });
  const page = await context.newPage();
  page.setDefaultTimeout(20_000);
  page.setDefaultNavigationTimeout(60_000);
  const frames: string[] = [];
  page.on("websocket", ws => ws.on("framereceived", frame => frames.push(String(frame.payload))));
  await page.goto("https://localhost:5174/entrar", { waitUntil: "domcontentloaded", timeout: 60_000 });
  await expect(page.getByRole("heading", { name: "Entrar", level: 1 })).toBeVisible();
  await page.evaluate(async csrf => {
    const response = await fetch("/api/panel/session");
    if (!response.ok) throw new Error(`fixture session: ${response.status}`);
    const data = await response.json();
    const { rememberSession } = await import(/* @vite-ignore */ "/src/api/panel.ts");
    rememberSession({ ...data, csrf_token: csrf });
    window.history.pushState({}, "", "/conversas");
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, actor.CSRF);
  await expect(page.getByRole("heading", { name: "Conversas", level: 1 })).toBeVisible();
  return { page, context, frames };
}

async function receive(fixture: Fixture, message: Record<string, unknown>) {
  const body = JSON.stringify({
    object: "whatsapp_business_account",
    entry: [{
      id: fixture.Waba,
      changes: [{
        field: "messages",
        value: {
          messaging_product: "whatsapp",
          metadata: { phone_number_id: fixture.Phone, display_phone_number: fixture.Phone },
          contacts: [{ profile: { name: "Cliente Simulado" }, wa_id: "5511988887104" }],
          messages: [{ from: "5511988887104", timestamp: String(Math.floor(Date.now() / 1000)), ...message }],
        },
      }],
    }],
  });
  const response = await fetch(`http://127.0.0.1:${process.env.CHATISP_E2E_API_PORT ?? "8080"}/api/webhooks/meta?app_id=${encodeURIComponent(fixture.App)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Hub-Signature-256": `sha256=${createHmac("sha256", fixture.Secret).update(body).digest("hex")}` },
    body,
  });
  expect(response.status).toBe(200);
}

function rows(page: Page) { return page.getByRole("list", { name: "Conversas encontradas" }).getByRole("button"); }

async function startReceiver() {
  const hits: HookHit[] = [];
  const server = createServer((request, response) => {
    const chunks: Buffer[] = [];
    request.on("data", (chunk) => chunks.push(Buffer.from(chunk)));
    request.on("end", () => {
      const body = Buffer.concat(chunks).toString("utf8");
      const typeHeader = request.headers["x-chatisp-event-type"];
      const signature = request.headers["x-chatisp-signature-256"];
      hits.push({
        type: Array.isArray(typeHeader) ? typeHeader[0] ?? "" : typeHeader ?? "",
        signature: Array.isArray(signature) ? signature[0] ?? "" : signature ?? "",
        body,
      });
      response.writeHead(204);
      response.end();
    });
  });
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve());
  });
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("receptor local sem porta");
  return {
    url: `http://127.0.0.1:${address.port}/m4b-hook`,
    hits,
    close: () => new Promise<void>((resolve) => server.close(() => resolve())),
  };
}

export async function runM4BM6(browser: Browser, testInfo: { attach(name: string, options: { body: string; contentType: string }): Promise<void> }) {
  const filename = process.env.M4B_FIXTURE_FILE;
  if (!filename) throw new Error("Fixture ausente: execute npm run test:e2e:m4b-m6; somente localhost. Sem skip.");
  const fixture: Fixture = JSON.parse(await readFile(filename, "utf8"));
  if (!fixture.M4B || !fixture.M4A || !fixture.M3B || fixture.M3B.Actors.length !== 3) throw new Error("Fixture M4B inválida");
  const agentActor = fixture.M3B.Actors[0]!;
  const receiver = await startReceiver();
  const admin = await openPanel(browser, fixture.M4A.Admin);
  const agent = await openPanel(browser, agentActor);
  const evidence: Record<string, string> = { simulation: fixture.M4B.Simulation, receiver: receiver.url };
  try {
    await admin.page.getByRole("link", { name: "Configurações", exact: true }).click();
    const hooks = admin.page.getByRole("region", { name: "Assinaturas de webhook" });
    await hooks.getByLabel("URL do webhook").fill(receiver.url);
    await hooks.getByRole("checkbox", { name: "Mensagem criada" }).check();
    await hooks.getByRole("button", { name: "Cadastrar assinatura" }).click();
    const secret = await hooks.getByLabel("Segredo do webhook").inputValue();
    expect(secret.length).toBeGreaterThan(16);
    evidence.webhook = "assinatura message.created cadastrada no painel para o receptor loopback";

    const inbox = agent.page.getByRole("combobox", { name: "Caixa de atendimento", exact: true });
    try {
      await inbox.waitFor({ state: "visible", timeout: 40_000 });
    } catch (error) {
      const detail = await agent.page.locator("body").innerText({ timeout: 2_000 }).catch(() => "");
      throw new Error(`painel sem caixa em ${agent.page.url()}: ${detail.slice(0, 500)}`, { cause: error });
    }
    await inbox.selectOption(fixture.M3B.Inbox);
    await agent.page.getByRole("tab", { name: "Todas", exact: true }).click();
    await expect.poll(() => agent.frames.some(frame => { try { return JSON.parse(frame).type === "snapshot"; } catch { return false; } })).toBe(true);
    await receive(fixture, { id: `wamid.simulation.image.${randomUUID()}`, type: "image", image: { id: fixture.M4B.ImageMediaID, mime_type: "image/jpeg", caption: "Fachada simulada" } });
    await receive(fixture, { id: `wamid.simulation.loc.${randomUUID()}`, type: "location", location: { latitude: -23.55, longitude: -46.63, name: "Sede simulada", address: "Rua da Simulação, 1" } });
    await expect(rows(agent.page)).toHaveCount(1);
    await rows(agent.page).first().click();
    const history = agent.page.getByRole("region", { name: "Histórico da conversa" });
    await expect(history.getByText("Fachada simulada")).toBeVisible();
    await expect(history.getByText("Imagem", { exact: true }).first()).toBeVisible();
    await expect(history.getByText("Sede simulada").first()).toBeVisible();
    await expect(history.getByText("Localização", { exact: true }).first()).toBeVisible();
    await expect(history.getByText("Coordenadas: -23.55, -46.63")).toBeVisible();
    evidence.inbound = "imagem e localização do stand-in Meta visíveis no painel";

    await agent.page.getByRole("button", { name: "Assumir", exact: true }).click();
    const location = history.getByRole("listitem", { name: /Localização de Cliente/ });
    await location.getByRole("button", { name: "Responder", exact: true }).click();
    const composer = agent.page.getByRole("form", { name: "Compositor de mensagem" });
    await expect(composer.getByText(/Respondendo a:/)).toBeVisible();
    await composer.locator("input[type=file]").setInputFiles({ name: "boleto.pdf", mimeType: "application/pdf", buffer: Buffer.from("%PDF-1.7\nsimulacao\n") });
    await expect(composer.getByText("Anexo pronto: boleto.pdf")).toBeVisible();
    const httpErrors: string[] = [];
    agent.page.on("response", (response) => {
      if (response.status() < 400 || !response.url().includes("/api/")) return;
      void response.text().then((body) => {
        httpErrors.push(`${response.status()} ${response.url()} ${body.slice(0, 240)}`);
      }).catch(() => undefined);
    });
    await composer.getByRole("button", { name: "Enviar", exact: true }).click();
    await expect.poll(async () => `${await composer.innerText()}\n${httpErrors.join("\n")}`).toContain("Mídia aceita pelo conector");
    const quotedDocument = history.getByRole("listitem", { name: /Documento de Atendimento/ });
    await expect.poll(async () => quotedDocument.innerText()).toContain("Mensagem citada: Sede simulada");
    evidence.quote = "documento de saída no histórico citando Sede simulada";

    await composer.getByRole("button", { name: "Gravar áudio" }).click();
    await composer.getByRole("button", { name: "Parar gravação" }).click();
    await composer.getByRole("button", { name: "Enviar áudio original" }).click();
    try {
      await expect(composer.getByText("Áudio original enviado. O navegador não converteu o arquivo.")).toBeVisible();
    } catch (error) {
      const shown = await composer.innerText({ timeout: 2_000 }).catch(() => "");
      throw new Error(`áudio não confirmado; compositor: ${shown.slice(0, 600)} | HTTP: ${httpErrors.slice(-5).join(" ; ")}`, { cause: error });
    }
    await expect(history.getByText("Áudio", { exact: true }).first()).toBeVisible();
    evidence.audio = "gravação simulada em audio/mpeg (tom sintético de 0,6 s) enviada pelo compositor; o servidor a converteu com o ffmpeg limitado";

    const consent = agent.page.getByRole("form", { name: "Consentimento do contato" });
    await consent.getByLabel("Finalidade do consentimento").fill("cobranca");
    await consent.getByLabel("Origem").fill("painel");
    await consent.getByLabel("Evidência").fill("evento:simulacao-m4b");
    await consent.getByRole("button", { name: "Registrar consentimento" }).click();
    await expect(consent.getByText("Concessão registrada.")).toBeVisible();

    const erp = agent.page.getByRole("region", { name: "ERP" });
    await erp.getByLabel("Cliente no ERP").fill(fixture.M4B.CustomerID);
    await erp.getByLabel("Contrato no ERP").fill(fixture.M4B.ContractID);
    await erp.getByRole("checkbox", { name: "Selecionar nesta conversa" }).check();
    await erp.getByRole("button", { name: "Vincular", exact: true }).click();
    await expect(erp.getByText("Cadastro simulado — casa")).toBeVisible();
    await erp.getByLabel("Verificação").fill("verificacao-simulada");
    await erp.getByLabel("Chave local das faturas").fill("faturas-m4b");
    await erp.getByRole("button", { name: "Consultar faturas" }).click();
    await expect(erp.getByText(fixture.M4B.InvoiceLabel, { exact: true })).toBeVisible();
    await erp.getByRole("button", { name: `Enviar documento ${fixture.M4B.InvoiceLabel}` }).click();
    const dialog = agent.page.getByRole("dialog", { name: "Confirmar segunda via" });
    await dialog.getByRole("button", { name: "Confirmar envio" }).click();
    await expect(erp.getByText(/Segunda via enfileirada/)).toBeVisible();
    evidence.erp = "resumo e fatura do ERP fake; segunda via enfileirada depois da confirmação";

    await expect.poll(() => {
      return receiver.hits.some((hit) => {
        if (hit.type !== "message.created") return false;
        const mac = `sha256=${createHmac("sha256", secret).update(hit.body).digest("hex")}`;
        if (hit.signature !== mac) return false;
        try { return JSON.parse(hit.body).type === "message.created"; } catch { return false; }
      });
    }, { timeout: 20_000 }).toBe(true);
    evidence.delivery = "receptor local aceitou message.created com HMAC sha256 do corpo";
    await testInfo.attach("m4b-m6-jornada", { body: JSON.stringify(evidence), contentType: "application/json" });
  } finally {
    await admin.context.close();
    await agent.context.close();
    await receiver.close();
  }
}
