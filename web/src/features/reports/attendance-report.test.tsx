import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { reportDirectoryKey, reportQueryKey } from "@/api/reports";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
import { renderPanel } from "@/test/render-panel";

const report = {
  timezone: "America/Sao_Paulo",
  from: "2026-11-03T03:00:00Z",
  to: "2026-11-04T03:00:00Z",
  group: "day",
  definitions: ["Relógios corridos, inclusive fora do expediente."],
  conversations: 1,
  cycles: 1,
  messages: 2,
  first_response: { sample: 0, not_applicable: 1, average_seconds: null, median_seconds: null },
  close_total: { sample: 1, not_applicable: 0, average_seconds: 14400, median_seconds: 14400 },
  close_operational: { sample: 1, not_applicable: 0, average_seconds: 3600, median_seconds: 3600 },
  queue_wait: { sample: 0, not_applicable: 0, average_seconds: null, median_seconds: null },
  results: [{ result: "resolved", author_kind: "bot", label: "Encerrado pelo bot", count: 1 }],
  sectors: [],
  attendants: [],
  inboxes: [],
  reasons: [{ name: "lentidão", count: 1, overlap: false }],
  tags: [{ name: "urgente", count: 1, overlap: true }],
  heatmap: [{ weekday: 2, hour: 8, cycles: 1 }],
  buckets: [{ key: "2026-11-03", cycles: 1, messages: 2, closed: 1, first_response_sample: 0, first_response_average_seconds: null, operational_sample: 1, operational_average_seconds: 3600 }],
  live: { in_queue: 2, in_attendance: 1, waiting_customer: 0, snoozed: 1, unassigned: 2, online_attendants: 3, longest_queue_wait_seconds: 1200, wait_alerts: 1, wait_alert_seconds: 900 },
  csv: "tempos,encerramento_operacional,1,3600,3600,0\n",
};

beforeEach(() => {
  resetSessionStore();
  useSessionStore.getState().applySession({
    tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role: "supervisor",
    email: "sup@example.test", expiresAt: "2099-01-01", mfaEnrollmentRequired: false,
  }, "csrf");
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo) => {
    const url = typeof input === "string" ? input : input.url;
    if (url.includes("/api/panel/reports")) {
      return new Response(JSON.stringify(report), { headers: { "content-type": "application/json" } });
    }
    return new Response("{}", { status: 503, headers: { "content-type": "application/problem+json" } });
  }));
});

it("mostra os tempos do período e a situação atual", async () => {
  renderPanel("/relatorios");
  expect(await screen.findByText("Relógios corridos, inclusive fora do expediente.")).toBeInTheDocument();
  expect(screen.getByText("Primeira resposta humana: não aplicável")).toBeInTheDocument();
  expect(screen.getByText("Tempo até o encerramento: 4 h (mediana 4 h, amostra 1)")).toBeInTheDocument();
  expect(screen.getByText("Tempo operacional: 1 h (mediana 1 h, amostra 1)")).toBeInTheDocument();
  expect(screen.getByText("Encerrado pelo bot: 1")).toBeInTheDocument();
  expect(screen.getByText("Estas contagens se sobrepõem e não fecham o total.")).toBeInTheDocument();
  expect(screen.getByText("Alertas de espera: 1")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Baixar CSV" })).toBeInTheDocument();
});

it("destaca a fila e a espera no painel", async () => {
  renderPanel("/painel");
  expect(await screen.findByText("Na fila: 2")).toBeInTheDocument();
  expect(screen.getByText("Sem responsável: 2")).toBeInTheDocument();
  expect(screen.getByText("Alertas de espera: 1")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Baixar CSV" })).not.toBeInTheDocument();
  expect(screen.queryByLabelText("Setor")).not.toBeInTheDocument();
});

const supervisor = {
  tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role: "supervisor",
  email: "sup@example.test", expiresAt: "2099-01-01", mfaEnrollmentRequired: false,
};

function requestUrl(input: RequestInfo): string {
  return typeof input === "string" ? input : input.url;
}

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { "content-type": "application/json" } });
}

function directories() {
  return {
    sectors: [{ id: "sec-1", name: "Financeiro", active: true }, { id: "sec-off", name: "Arquivado", active: false }],
    inboxes: [{ id: "box-1", name: "WhatsApp" }],
  };
}

it("filtra por setor e caixa autorizados e isola a chave da sessão", async () => {
  const calls: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo) => {
    const url = requestUrl(input);
    calls.push(url);
    if (url.includes("/api/panel/sectors")) return json({ sectors: directories().sectors });
    if (url.includes("/api/panel/inboxes")) return json({ inboxes: directories().inboxes });
    if (url.includes("/api/panel/reports")) {
      const parsed = new URL(url, "http://panel.local");
      const sector = parsed.searchParams.get("sector_id") ?? "";
      const inbox = parsed.searchParams.get("inbox_id") ?? "";
      return json({
        ...report,
        definitions: [`recorte ${sector || "todos"} ${inbox || "todas"}`],
        sectors: [{ sector_id: "sec-1", name: "Financeiro", cycles: 3, queue_waits: 2, queue_wait_median_seconds: 45, responsibility_seconds: 120 }],
        attendants: [{ membership_id: "mem-1", label: "Ana", responsibility_seconds: 60, messages: 4, closures: 1 }],
        inboxes: [{ inbox_id: "box-1", name: "WhatsApp", cycles: 3, messages: 5 }],
        csv: `csv-${sector || "todos"}-${inbox || "todas"}\n`,
      });
    }
    return new Response("{}", { status: 503, headers: { "content-type": "application/problem+json" } });
  }));
  const user = userEvent.setup();
  const view = renderPanel("/relatorios");
  expect(await screen.findByRole("option", { name: "Financeiro" })).toBeInTheDocument();
  expect(screen.queryByRole("option", { name: "Arquivado" })).not.toBeInTheDocument();
  expect(screen.queryByRole("textbox", { name: /uuid|identificador/i })).not.toBeInTheDocument();
  const from = (screen.getByLabelText("Início") as HTMLInputElement).value;
  const to = (screen.getByLabelText("Fim") as HTMLInputElement).value;
  await user.selectOptions(screen.getByLabelText("Setor"), "sec-1");
  await screen.findByText("recorte sec-1 todas");
  await user.selectOptions(screen.getByLabelText("Caixa"), "box-1");
  await screen.findByText("recorte sec-1 box-1");
  expect(calls.some((url) => url.includes("sector_id=sec-1") && url.includes("inbox_id=box-1"))).toBe(true);
  expect(calls.some((url) => url.includes("/api/panel/reports") && !url.includes("sector_id="))).toBe(true);
  const key = reportQueryKey(supervisor, "full", { from, to, group: "day", sectorId: "sec-1", inboxId: "box-1" });
  expect(view.queryClient.getQueryCache().find({ queryKey: key })?.state.status).toBe("success");
  expect(screen.getByText("Financeiro: 3 ciclos, 2 esperas, mediana 45 s, responsabilidade 2 min")).toBeInTheDocument();
  expect(screen.getByText("Ana: responsabilidade 1 min, 4 mensagens, 1 encerramento")).toBeInTheDocument();
  expect(screen.getByText("WhatsApp: 3 ciclos, 5 mensagens")).toBeInTheDocument();
  expect(screen.queryByText(/taxa/i)).not.toBeInTheDocument();
  act(() => useSessionStore.getState().applySession({ ...supervisor, sessionId: "s2" }, "csrf"));
  await screen.findByText("recorte todos todas");
  expect(screen.queryByText("recorte sec-1 box-1")).not.toBeInTheDocument();
  const other = reportQueryKey({ ...supervisor, sessionId: "s2" }, "full", { from, to, group: "day" });
  const otherData = view.queryClient.getQueryCache().find({ queryKey: other })?.state.data;
  expect(otherData).toMatchObject({ definitions: ["recorte todos todas"] });
  expect(JSON.stringify(otherData)).not.toContain("csv-sec-1-box-1");
  expect(JSON.stringify(view.queryClient.getQueryCache().find({ queryKey: key })?.state.data)).toContain("csv-sec-1-box-1");
});

it("mostra a grade de sete dias por vinte e quatro horas", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo) => {
    const url = requestUrl(input);
    if (url.includes("/api/panel/sectors")) return json({ sectors: [] });
    if (url.includes("/api/panel/inboxes")) return json({ inboxes: [] });
    if (url.includes("/api/panel/reports")) {
      return json({
        ...report,
        conversations: 0,
        cycles: 0,
        messages: 0,
        heatmap: [{ weekday: 2, hour: 8, cycles: 1 }, { weekday: 2, hour: 9, cycles: 0 }, { weekday: 9, hour: 4, cycles: 7 }],
      });
    }
    return new Response("{}", { status: 503, headers: { "content-type": "application/problem+json" } });
  }));
  renderPanel("/relatorios");
  expect(await screen.findByRole("table", { name: /Mapa de calor no fuso America\/Sao_Paulo/ })).toBeInTheDocument();
  expect(screen.getAllByRole("cell")).toHaveLength(168);
  expect(screen.getByRole("cell", { name: "terça, 8 horas, 1 ciclo" })).toHaveTextContent("1");
  expect(screen.getByRole("cell", { name: "terça, 9 horas, zero ciclos" })).toHaveTextContent(/^0$/);
  expect(screen.getByRole("cell", { name: "domingo, 0 horas, sem registro" })).toHaveTextContent("sem registro");
  expect(screen.getByText("Zero: o cálculo devolveu 0 ciclos. Não é ausência de resposta.")).toBeInTheDocument();
  expect(screen.getByText("Nenhum ciclo, conversa ou mensagem neste recorte.")).toBeInTheDocument();
  expect(screen.queryByRole("cell", { name: /7 ciclos/ })).not.toBeInTheDocument();
});

it("não exporta o relatório anterior na troca nem depois da recusa", async () => {
  const pending: { url: string; resolve: (response: Response) => void }[] = [];
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo) => {
    const url = requestUrl(input);
    if (url.includes("/api/panel/sectors")) return Promise.resolve(json({ sectors: directories().sectors }));
    if (url.includes("/api/panel/inboxes")) return Promise.resolve(json({ inboxes: directories().inboxes }));
    if (!url.includes("/api/panel/reports")) return Promise.resolve(new Response("{}", { status: 503 }));
    return new Promise<Response>((resolve) => { pending.push({ url, resolve }); });
  }));
  const blobs: Blob[] = [];
  Object.defineProperty(URL, "createObjectURL", { configurable: true, writable: true, value: (blob: Blob) => { blobs.push(blob); return "blob:csv"; } });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, writable: true, value: () => undefined });
  const user = userEvent.setup();
  renderPanel("/relatorios");
  await screen.findByRole("option", { name: "Financeiro" });
  const first = pending[0];
  expect(first).toBeDefined();
  first?.resolve(json({ ...report, definitions: ["marca antiga"], csv: "csv-antigo\n", heatmap: [] }));
  expect(await screen.findByText("marca antiga")).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("Setor"), "sec-1");
  await waitFor(() => expect(pending.some((item) => item.url.includes("sector_id=sec-1"))).toBe(true));
  expect(screen.queryByText("marca antiga")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Baixar CSV" })).not.toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("Caixa"), "box-1");
  await waitFor(() => expect(pending.some((item) => item.url.includes("inbox_id=box-1"))).toBe(true));
  await act(async () => {
    pending.find((item) => item.url.includes("sector_id=sec-1") && !item.url.includes("inbox_id="))?.resolve(json({ ...report, definitions: ["marca proibida"], csv: "csv-proibido\n" }));
  });
  expect(screen.queryByText("marca proibida")).not.toBeInTheDocument();
  expect(screen.queryByText("marca antiga")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Baixar CSV" })).not.toBeInTheDocument();
  const latest = pending.find((item) => item.url.includes("sector_id=sec-1") && item.url.includes("inbox_id=box-1"));
  expect(latest).toBeDefined();
  await act(async () => {
    latest?.resolve(new Response(JSON.stringify({ title: "Acesso negado" }), { status: 403, headers: { "content-type": "application/problem+json" } }));
  });
  expect(await screen.findByText("Acesso negado.")).toBeInTheDocument();
  expect(screen.queryByText("marca antiga")).not.toBeInTheDocument();
  expect(screen.queryByText("marca proibida")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Baixar CSV" })).not.toBeInTheDocument();
  expect(blobs).toHaveLength(0);
  await user.selectOptions(screen.getByLabelText("Setor"), "");
  await waitFor(() => expect(pending.some((item) => item.url.includes("inbox_id=box-1") && !item.url.includes("sector_id="))).toBe(true));
  pending.find((item) => item.url.includes("inbox_id=box-1") && !item.url.includes("sector_id="))?.resolve(json({ ...report, definitions: ["marca nova"], csv: "csv-novo\n", heatmap: [] }));
  expect(await screen.findByText("marca nova")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Baixar CSV" }));
  expect(blobs).toHaveLength(1);
  await expect(blobs[0]?.text()).resolves.toBe("csv-novo\n");
});

function deniedResponse() {
  return new Response(JSON.stringify({ title: "Acesso negado" }), { status: 403, headers: { "content-type": "application/problem+json" } });
}

function slice(marker: string, cycles: number, csv: string, queue: number) {
  const sector = `Nome ${marker}`;
  const attendant = `Atendente ${marker}`;
  const inbox = `Caixa ${marker}`;
  return {
    body: {
      ...report,
      definitions: [`definição ${marker}`],
      conversations: cycles,
      cycles,
      messages: cycles,
      heatmap: [],
      live: { ...report.live, in_queue: queue },
      csv,
      sectors: [{ sector_id: "sec-1", name: sector, cycles, queue_waits: 1, queue_wait_median_seconds: 10, responsibility_seconds: 20 }],
      attendants: [{ membership_id: "mem-1", label: attendant, responsibility_seconds: 30, messages: 2, closures: 1 }],
      inboxes: [{ inbox_id: "box-1", name: inbox, cycles, messages: 2 }],
    },
    visible: [
      `definição ${marker}`,
      `${sector}: ${cycles} ciclos, 1 esperas, mediana 10 s, responsabilidade 20 s`,
      `${attendant}: responsabilidade 30 s, 2 mensagens, 1 encerramento`,
      `${inbox}: ${cycles} ciclos, 2 mensagens`,
      `Na fila: ${queue}`,
    ],
  };
}

function expectTextsHidden(texts: string[]) {
  for (const text of texts) expect(screen.queryByText(text)).not.toBeInTheDocument();
}

function expectExportHidden(texts: string[]) {
  expectTextsHidden(texts);
  expect(screen.queryByRole("button", { name: "Baixar CSV" })).not.toBeInTheDocument();
}

it("não mostra o cache antigo ao voltar para uma seleção em atualização", async () => {
  const pending: { url: string; resolve: (response: Response) => void }[] = [];
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo) => {
    const url = requestUrl(input);
    if (url.includes("/api/panel/sectors")) return Promise.resolve(json({ sectors: directories().sectors }));
    if (url.includes("/api/panel/inboxes")) return Promise.resolve(json({ inboxes: directories().inboxes }));
    if (!url.includes("/api/panel/reports")) return Promise.resolve(new Response("{}", { status: 503 }));
    return new Promise<Response>((resolve) => { pending.push({ url, resolve }); });
  }));
  const blobs: Blob[] = [];
  Object.defineProperty(URL, "createObjectURL", { configurable: true, writable: true, value: (blob: Blob) => { blobs.push(blob); return "blob:csv"; } });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, writable: true, value: () => undefined });
  const broad = slice("A", 8, "csv-antigo-setor\n", 8);
  const filtered = slice("B", 3, "csv-b\n", 3);
  const validated = slice("validada", 6, "csv-validado\n", 6);
  const user = userEvent.setup();
  const view = renderPanel("/relatorios");
  await screen.findByRole("option", { name: "Financeiro" });
  const from = (screen.getByLabelText("Início") as HTMLInputElement).value;
  const to = (screen.getByLabelText("Fim") as HTMLInputElement).value;
  const broadKey = reportQueryKey(supervisor, "full", { from, to, group: "day", sectorId: "", inboxId: "" });
  await waitFor(() => expect(pending.some((item) => item.url.includes("/api/panel/reports") && !item.url.includes("sector_id="))).toBe(true));
  await act(async () => { pending[0]?.resolve(json(broad.body)); });
  expect(await screen.findByText("definição A")).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("Setor"), "sec-1");
  await waitFor(() => expect(pending.some((item) => item.url.includes("sector_id=sec-1"))).toBe(true));
  expectExportHidden(broad.visible);
  await act(async () => { pending.find((item) => item.url.includes("sector_id=sec-1"))?.resolve(json(filtered.body)); });
  expect(await screen.findByText("definição B")).toBeInTheDocument();
  await act(async () => { await view.queryClient.invalidateQueries({ queryKey: broadKey, refetchType: "none" }); });
  await user.selectOptions(screen.getByLabelText("Setor"), "");
  await waitFor(() => expect(pending.filter((item) => item.url.includes("/api/panel/reports") && !item.url.includes("sector_id="))).toHaveLength(2));
  expectExportHidden(broad.visible);
  expectExportHidden(filtered.visible);
  const stale = pending.filter((item) => item.url.includes("/api/panel/reports") && !item.url.includes("sector_id="))[1];
  await act(async () => { stale?.resolve(deniedResponse()); });
  expect(await screen.findByText("Acesso negado.")).toBeInTheDocument();
  expectExportHidden(broad.visible);
  expectExportHidden(filtered.visible);
  expect(blobs).toHaveLength(0);
  await user.selectOptions(screen.getByLabelText("Setor"), "sec-1");
  expect(await screen.findByText("definição B")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Baixar CSV" })).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("Setor"), "");
  await waitFor(() => expect(pending.filter((item) => item.url.includes("/api/panel/reports") && !item.url.includes("sector_id="))).toHaveLength(3));
  expectExportHidden(broad.visible);
  expectExportHidden(filtered.visible);
  expect(screen.queryByText("definição validada")).not.toBeInTheDocument();
  const retry = pending.filter((item) => item.url.includes("/api/panel/reports") && !item.url.includes("sector_id="))[2];
  await act(async () => { retry?.resolve(json(validated.body)); });
  expect(await screen.findByText("definição validada")).toBeInTheDocument();
  expect(screen.getByText("Nome validada: 6 ciclos, 1 esperas, mediana 10 s, responsabilidade 20 s")).toBeInTheDocument();
  expectTextsHidden(broad.visible);
  await user.click(screen.getByRole("button", { name: "Baixar CSV" }));
  expect(blobs).toHaveLength(1);
  await expect(blobs[0]?.text()).resolves.toBe("csv-validado\n");
});

it.each([
  ["setores", "sectors", "Setor", "sec-1", "sector_id"] as const,
  ["caixas", "inboxes", "Caixa", "box-1", "inbox_id"] as const,
])("retira relatório e CSV do cache quando a lista de %s passa a responder 403", async (kind, directory, label, optionId, param) => {
  const pending: { url: string; resolve: (response: Response) => void }[] = [];
  let sectorsStatus = 200;
  let inboxesStatus = 200;
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo) => {
    const url = requestUrl(input);
    if (url.includes("/api/panel/sectors")) {
      return Promise.resolve(sectorsStatus === 403 ? deniedResponse() : json({ sectors: directories().sectors }));
    }
    if (url.includes("/api/panel/inboxes")) {
      return Promise.resolve(inboxesStatus === 403 ? deniedResponse() : json({ inboxes: directories().inboxes }));
    }
    if (!url.includes("/api/panel/reports")) return Promise.resolve(new Response("{}", { status: 503 }));
    return new Promise<Response>((resolve) => { pending.push({ url, resolve }); });
  }));
  const blobs: Blob[] = [];
  Object.defineProperty(URL, "createObjectURL", { configurable: true, writable: true, value: (blob: Blob) => { blobs.push(blob); return "blob:csv"; } });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, writable: true, value: () => undefined });
  const broad = slice("amplo", 9, "csv-amplo\n", 9);
  const filtered = slice("filtrado", 4, "csv-filtrado\n", 4);
  const recovered = slice("recuperado", 2, "csv-recuperado\n", 2);
  const user = userEvent.setup();
  const view = renderPanel("/relatorios");
  await screen.findByRole("option", { name: "Financeiro" });
  await waitFor(() => expect(pending).toHaveLength(1));
  await act(async () => { pending[0]?.resolve(json(broad.body)); });
  expect(await screen.findByText("definição amplo")).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText(label), optionId);
  await waitFor(() => expect(pending.some((item) => item.url.includes(`${param}=${optionId}`))).toBe(true));
  expectExportHidden(broad.visible);
  await act(async () => { pending.find((item) => item.url.includes(`${param}=${optionId}`))?.resolve(json(filtered.body)); });
  expect(await screen.findByText("definição filtrado")).toBeInTheDocument();
  const beforeDenial = pending.length;
  if (directory === "sectors") sectorsStatus = 403;
  else inboxesStatus = 403;
  await act(async () => { await view.queryClient.invalidateQueries({ queryKey: reportDirectoryKey(directory, supervisor) }); });
  expect(await screen.findByText(`Acesso negado à lista de ${kind}.`)).toBeInTheDocument();
  expectExportHidden([...broad.visible, ...filtered.visible]);
  expect(pending).toHaveLength(beforeDenial);
  expect(blobs).toHaveLength(0);
  if (directory === "sectors") sectorsStatus = 200;
  else inboxesStatus = 200;
  await act(async () => { await view.queryClient.invalidateQueries({ queryKey: reportDirectoryKey(directory, supervisor) }); });
  expect(await screen.findByRole("option", { name: directory === "sectors" ? "Financeiro" : "WhatsApp" })).toBeInTheDocument();
  await waitFor(() => expect(pending.length).toBeGreaterThan(beforeDenial));
  expectExportHidden([...broad.visible, ...filtered.visible]);
  const recovery = pending[pending.length - 1];
  expect(recovery?.url.includes(`${param}=`)).toBe(false);
  await act(async () => { recovery?.resolve(json(recovered.body)); });
  expect(await screen.findByText("definição recuperado")).toBeInTheDocument();
  expect(screen.getByText("Nome recuperado: 2 ciclos, 1 esperas, mediana 10 s, responsabilidade 20 s")).toBeInTheDocument();
  expectTextsHidden([...broad.visible, ...filtered.visible]);
  await user.click(screen.getByRole("button", { name: "Baixar CSV" }));
  expect(blobs).toHaveLength(1);
  await expect(blobs[0]?.text()).resolves.toBe("csv-recuperado\n");
});
