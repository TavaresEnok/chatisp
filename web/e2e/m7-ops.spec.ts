import { expect as playwrightExpect, type Browser, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";

const expect = playwrightExpect.configure({ timeout: 20_000 });

type Actor = { Token: string; CSRF: string; User: string; Member: string; Role: string };
type Box = { Sector: string; Inbox: string; SectorName: string; InboxName: string; Cycles: number; Messages: number };
type M7 = {
  Admin: Actor;
  Restricted: Actor;
  Alfa: Box;
  Beta: Box;
  From: string;
  To: string;
  Timezone: string;
  PanelOrigin: string;
  API: string;
  MetricsURL: string;
  ControlURL: string;
  ControlToken: string;
  HeatWeekday: number;
  HeatHour: number;
};
type Fixture = { Email: string; Secret: string; Password: string; M7: M7 };

const weekdays = ["domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"];
const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");
const brandName = "Provedor M7 Simulado";

function heatLabel(weekday: number, hour: number, cycles: number): string {
  const day = weekdays[weekday] ?? "dia";
  if (cycles <= 0) return `${day}, ${hour} horas, sem registro`;
  return `${day}, ${hour} horas, ${cycles} ${cycles === 1 ? "ciclo" : "ciclos"}`;
}

function inboxLine(box: Box): string {
  return `${box.InboxName}: ${box.Cycles} ciclos, ${box.Messages} mensagens`;
}

async function openPanel(browser: Browser, actor: Actor, origin: string, path: string) {
  const context = await browser.newContext({ ignoreHTTPSErrors: true, acceptDownloads: true });
  await context.addCookies([{ name: "__Host-session", value: actor.Token, url: origin, secure: true, httpOnly: true, sameSite: "Strict" }]);
  const page = await context.newPage();
  page.setDefaultTimeout(20_000);
  page.setDefaultNavigationTimeout(30_000);
  await page.goto(`${origin}/entrar`, { waitUntil: "domcontentloaded", timeout: 30_000 });
  await expect(page.getByRole("heading", { name: "Entrar", level: 1 })).toBeVisible();
  await page.evaluate(async ({ csrf, path: next }) => {
    const response = await fetch("/api/panel/session");
    if (!response.ok) throw new Error(`fixture session: ${response.status}`);
    const data = await response.json();
    const { rememberSession } = await import(/* @vite-ignore */ "/src/api/panel.ts");
    rememberSession({ ...data, csrf_token: csrf });
    window.history.pushState({}, "", next);
    window.dispatchEvent(new PopStateEvent("popstate"));
  }, { csrf: actor.CSRF, path });
  return { page, context };
}

async function setRange(page: Page, from: string, to: string) {
  await page.getByLabel("Início").fill(from);
  await page.getByLabel("Fim").fill(to);
}

function sample(body: string, name: string, ...needles: string[]): number | null {
  const lines = body.split("\n").filter((line) => line.startsWith(name) && !line.startsWith("#") && needles.every((needle) => line.includes(needle)));
  if (lines.length !== 1) return null;
  const value = Number(lines[0]?.trim().split(/\s+/).at(-1));
  return Number.isFinite(value) ? value : null;
}

export async function runM7Ops(browser: Browser, testInfo: { attach(name: string, options: { body: string; contentType: string }): Promise<void> }) {
  const filename = process.env.M7_FIXTURE_FILE;
  if (!filename) throw new Error("Fixture ausente: execute npm run test:e2e:m7-ops; somente localhost. Sem skip.");
  const fixture: Fixture = JSON.parse(await readFile(filename, "utf8"));
  const m7 = fixture.M7;
  if (!m7?.Admin?.Token || !m7.Restricted?.Token || !m7.Alfa?.Sector || !m7.Beta?.Sector || !m7.PanelOrigin.startsWith("https://127.0.0.1:") || !m7.MetricsURL.startsWith("http://127.0.0.1:") || !m7.ControlURL.startsWith("http://127.0.0.1:") || !m7.ControlToken) {
    throw new Error("Fixture M7 inválida");
  }
  const evidence: Record<string, string> = {
    code: process.env.M7_CODE_REV ?? "",
    browser: process.env.M7_BROWSER_VERSION ?? "",
    panel: m7.PanelOrigin,
    api: m7.API,
    metricsUrl: m7.MetricsURL,
    range: `${m7.From}..${m7.To}`,
    timezone: m7.Timezone,
  };
  const admin = await openPanel(browser, m7.Admin, m7.PanelOrigin, "/configuracoes");
  const reports: string[] = [];
  admin.page.on("response", (response) => {
    if (response.status() === 200 && response.url().includes("/api/panel/reports")) {
      void response.text().then((body) => reports.push(body)).catch(() => undefined);
    }
  });
  try {
    await expect(admin.page.getByRole("heading", { name: "Configurações", level: 1 })).toBeVisible();
    await expect(admin.page.getByRole("heading", { name: "Identidade do provedor" })).toBeVisible();
    await admin.page.getByLabel("Nome exibido do provedor").fill(brandName);
    await admin.page.getByLabel("Logo do provedor").setInputFiles({ name: "marca.png", mimeType: "image/png", buffer: png });
    await admin.page.getByRole("button", { name: "Guardar identidade" }).click();
    await expect(admin.page.getByText("Identidade guardada. Os prazos de retenção não foram alterados.")).toBeVisible();
    await expect(admin.page.getByRole("img", { name: `Logo de ${brandName}` })).toBeVisible();
    await expect(admin.page).toHaveTitle(new RegExp(`${brandName}$`));
    evidence.brand = "admin gravou nome e PNG local; o shell mostrou os dois";

    await admin.page.getByRole("link", { name: "Relatórios", exact: true }).click();
    await expect(admin.page.getByRole("heading", { name: "Relatórios", level: 1 })).toBeVisible();
    await setRange(admin.page, m7.From, m7.To);
    await expect(admin.page.getByText(inboxLine(m7.Alfa))).toBeVisible();
    await expect(admin.page.getByText(inboxLine(m7.Beta))).toBeVisible();
    await expect(admin.page.getByRole("cell", { name: heatLabel(m7.HeatWeekday, m7.HeatHour, m7.Alfa.Cycles + m7.Beta.Cycles) })).toBeVisible();
    await admin.page.getByLabel("Setor", { exact: true }).selectOption(m7.Alfa.Sector);
    await expect(admin.page.getByText(inboxLine(m7.Alfa))).toBeVisible();
    await expect(admin.page.getByText(inboxLine(m7.Beta))).toHaveCount(0);
    await expect(admin.page.getByText(`Setor Alfa M7: ${m7.Alfa.Cycles} ciclos`)).toBeVisible();
    await expect(admin.page.getByRole("cell", { name: heatLabel(m7.HeatWeekday, m7.HeatHour, m7.Alfa.Cycles) })).toBeVisible();
    const alfaDownload = admin.page.waitForEvent("download");
    await admin.page.getByRole("button", { name: "Baixar CSV" }).click();
    const alfaFile = await (await alfaDownload).path();
    if (!alfaFile) throw new Error("CSV do recorte Alfa sem arquivo");
    const alfaCsv = await readFile(alfaFile, "utf8");
    for (const line of [`resumo,ciclos,${m7.Alfa.Cycles}`, `resumo,mensagens,${m7.Alfa.Messages}`, `caixa,${m7.Alfa.InboxName},${m7.Alfa.Cycles},${m7.Alfa.Messages}`, `calor,${weekdays[m7.HeatWeekday]},${m7.HeatHour},${m7.Alfa.Cycles}`]) {
      if (!alfaCsv.includes(line)) throw new Error(`CSV Alfa sem ${line}`);
    }
    if (alfaCsv.includes(m7.Beta.InboxName)) throw new Error("CSV Alfa trouxe a outra caixa");
    await admin.page.getByLabel("Caixa", { exact: true }).selectOption(m7.Beta.Inbox);
    await expect(admin.page.getByText("Nenhum ciclo, conversa ou mensagem neste recorte.")).toBeVisible();
    await expect(admin.page.getByText(inboxLine(m7.Alfa))).toHaveCount(0);
    await expect(admin.page.getByRole("cell", { name: heatLabel(m7.HeatWeekday, m7.HeatHour, 0) })).toBeVisible();
    await admin.page.getByLabel("Setor", { exact: true }).selectOption("");
    await expect(admin.page.getByText(inboxLine(m7.Beta))).toBeVisible();
    await expect(admin.page.getByText(inboxLine(m7.Alfa))).toHaveCount(0);
    await expect(admin.page.getByRole("cell", { name: heatLabel(m7.HeatWeekday, m7.HeatHour, m7.Beta.Cycles) })).toBeVisible();
    const betaDownload = admin.page.waitForEvent("download");
    await admin.page.getByRole("button", { name: "Baixar CSV" }).click();
    const betaFile = await (await betaDownload).path();
    if (!betaFile) throw new Error("CSV do recorte Beta sem arquivo");
    const betaCsv = await readFile(betaFile, "utf8");
    for (const line of [`resumo,ciclos,${m7.Beta.Cycles}`, `resumo,mensagens,${m7.Beta.Messages}`, `caixa,${m7.Beta.InboxName},${m7.Beta.Cycles},${m7.Beta.Messages}`]) {
      if (!betaCsv.includes(line)) throw new Error(`CSV Beta sem ${line}`);
    }
    if (betaCsv.includes(m7.Alfa.InboxName)) throw new Error("CSV Beta trouxe a outra caixa");
    evidence.report = `filtros ${m7.From} setor/caixa Alfa ${m7.Alfa.Cycles}/${m7.Alfa.Messages} e Beta ${m7.Beta.Cycles}/${m7.Beta.Messages}; grade e CSV coincidiram`;

    await admin.page.getByRole("link", { name: "Conversas", exact: true }).click();
    await expect(admin.page.getByRole("heading", { name: "Conversas", level: 1 })).toBeVisible();
    const socket = admin.page.waitForEvent("websocket", { timeout: 20_000 });
    await admin.page.getByRole("combobox", { name: "Caixa de atendimento", exact: true }).selectOption(m7.Alfa.Inbox);
    await socket;
    const metrics = await readMetrics(fixture);
    evidence.metrics = `pools app/platform/ingress/jobs, websocket ${metrics.sockets}, river ok=${metrics.riverOk} fila ingress/available=${metrics.ingressJobs}`;
  } finally {
    await admin.context.close();
  }

  const restricted = await openPanel(browser, m7.Restricted, m7.PanelOrigin, "/configuracoes");
  try {
    await expect(restricted.page.getByRole("heading", { name: "Configurações", level: 1 })).toBeVisible();
    await expect(restricted.page.getByRole("heading", { name: "Identidade do provedor" })).toHaveCount(0);
    await expect(restricted.page.getByLabel("Nome exibido do provedor")).toHaveCount(0);
    await expect(restricted.page.getByText(brandName).first()).toBeVisible();
    const denied = await restricted.page.evaluate(async (csrf) => {
      const response = await fetch("/api/panel/branding", {
        method: "PUT",
        headers: { "Content-Type": "application/json", "X-CSRF-Token": csrf },
        body: JSON.stringify({ display_name: "Nome proibido M7" }),
      });
      return response.status;
    }, m7.Restricted.CSRF);
    if (denied !== 403) throw new Error(`edição sem permissão retornou ${denied}`);
    await expect(restricted.page.getByText("Nome proibido M7")).toHaveCount(0);
    evidence.restricted = "agente sem editor; PUT da identidade retornou 403 e o nome gravado permaneceu";

    await restricted.page.getByRole("link", { name: "Relatórios", exact: true }).click();
    await expect(restricted.page.getByRole("heading", { name: "Relatórios", level: 1 })).toBeVisible();
    await setRange(restricted.page, m7.From, m7.To);
    const kept = inboxLine(m7.Alfa);
    await expect(restricted.page.getByText(kept)).toBeVisible();
    await expect(restricted.page.getByText(inboxLine(m7.Beta))).toBeVisible();
    await expect(restricted.page.getByRole("option", { name: m7.Beta.SectorName })).toHaveCount(1);
    // The option stays in the loaded directory. Removing the membership makes the panel's own next filter a real 403.
    const revoke = await fetch(m7.ControlURL, { method: "POST", headers: { "X-M7-Control": m7.ControlToken } });
    if (revoke.status !== 204) throw new Error(`revogação local retornou ${revoke.status}`);
    const forbidden = restricted.page.waitForResponse((response) => response.url().includes("/api/panel/reports") && response.url().includes(`sector_id=${m7.Beta.Sector}`));
    await restricted.page.getByLabel("Setor", { exact: true }).selectOption(m7.Beta.Sector);
    if ((await forbidden).status() !== 403) throw new Error("filtro alheio não retornou 403");
    await expect(restricted.page.getByText("Acesso negado.")).toBeVisible();
    await expect(restricted.page.getByText(kept)).toHaveCount(0);
    await expect(restricted.page.getByRole("button", { name: "Baixar CSV" })).toHaveCount(0);
    evidence.denied = "403 do setor alheio retirou a grade e o CSV anteriores";
  } finally {
    await restricted.context.close();
  }
  await testInfo.attach("m7-ops-jornada", { body: JSON.stringify(evidence, null, 2), contentType: "application/json" });
}

async function readMetrics(fixture: Fixture) {
  const m7 = fixture.M7;
  let body = "";
  for (let attempt = 0; attempt < 40; attempt++) {
    const response = await fetch(m7.MetricsURL);
    body = await response.text();
    if (!response.ok) throw new Error(`métricas HTTP ${response.status}`);
    const sockets = sample(body, "chatisp_websocket_connections");
    const riverOk = sample(body, "chatisp_river_collect_ok");
    const ingressJobs = sample(body, "chatisp_river_jobs", 'queue="ingress"', 'state="available"');
    const pools = ["app", "platform", "ingress", "jobs"].map((role) => sample(body, "chatisp_db_pool_max_connections", `role="${role}"`));
    if (sockets !== null && sockets >= 1 && riverOk === 1 && ingressJobs !== null && pools.every((value) => value !== null && value >= 1)) {
      const apiMetrics = await fetch(`${m7.API}/metrics`);
      const apiBody = await apiMetrics.text();
      if (apiMetrics.status === 200 && apiBody.includes("chatisp_db_pool_max_connections")) {
        throw new Error("a API pública serviu as séries privadas");
      }
      for (const needle of ["postgres://", "password=", "example.test", "chatisp_app", "chatisp_jobs", "chatisp_platform", "chatisp_ingress", "sim-app-", "5511999990001", fixture.Email, fixture.Secret, fixture.Password, m7.Admin.Token, m7.Restricted.Token]) {
        if (needle && body.includes(needle)) throw new Error("métrica expôs dado de sessão, DSN ou identidade");
      }
      return { sockets, riverOk, ingressJobs };
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`séries reais ausentes em /metrics: ${body.slice(0, 400)}`);
}
