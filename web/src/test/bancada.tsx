/*
  BANCADA VISUAL — só para desenvolvimento. Não entra no build de produção (a entrada é web/bancada.html).

  Renderiza as telas reais do painel com a API substituída por respostas sintéticas no navegador,
  para conferir a aparência sem subir o backend. Tudo aqui é SIMULAÇÃO: nomes, protocolos e mensagens
  são fictícios, nenhuma rota real é chamada e nada do que aparece comprova entrega, canal ou integração.

  Uso: npm run dev e abrir /bancada.html?rota=/conversas&papel=admin&tema=dark&menu=collapsed
*/
import { QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router";
import { createQueryClient } from "@/lib/query-client";
import { SessionQuerySync } from "@/providers/session-query-sync";
import { AppRoutes } from "@/routes";
import { syncInterfaceToDocument, useInterfaceStore } from "@/stores/interface-store";
import { useSessionStore } from "@/stores/session-store";
import "../index.css";

const params = new URLSearchParams(window.location.search);
const route = params.get("rota") ?? "/conversas";
const role = (params.get("papel") ?? "admin") as "admin" | "supervisor" | "agent";
const signedOut = params.get("sessao") === "nenhuma";

const me = "m-clara";
const now = Date.now();
const at = (minutesAgo: number) => new Date(now - minutesAgo * 60_000).toISOString();

const sectors = [
  { id: "s-suporte", name: "Suporte", active: true },
  { id: "s-financeiro", name: "Financeiro", active: true },
  { id: "s-comercial", name: "Comercial", active: true },
];
const people = [
  { membership_id: me, label: "clara.mendes@provedor.example" },
  { membership_id: "m-rafael", label: "rafael.lima@provedor.example" },
  { membership_id: "m-bianca", label: "bianca.sato@provedor.example" },
];
const heads = [
  { id: "c1", protocol: "AT-2048", contact_id: "k1", contact_label: "Mariana Costa", assignee_id: me, status: "open", sector_id: "s-suporte", priority: "urgent", unread: true, unread_count: 1, last: 4, wait_alert: false },
  { id: "c2", protocol: "AT-2051", contact_id: "k2", contact_label: "Rafael Nogueira", assignee_id: "", status: "triage", sector_id: "s-suporte", priority: "normal", unread: true, unread_count: 2, last: 6, wait_alert: true },
  { id: "c3", protocol: "AT-2044", contact_id: "k3", contact_label: "Helena Prado", assignee_id: me, status: "waiting_customer", sector_id: "s-comercial", priority: "normal", unread: false, unread_count: 0, last: 10, wait_alert: false },
  { id: "c4", protocol: "AT-2039", contact_id: "k4", contact_label: "Lucas Almeida", assignee_id: "m-rafael", status: "open", sector_id: "s-financeiro", priority: "normal", unread: false, unread_count: 0, last: 17, wait_alert: false },
  { id: "c5", protocol: "AT-2052", contact_id: "k5", contact_label: "Beatriz Siqueira", assignee_id: "", status: "triage", sector_id: "s-comercial", priority: "normal", unread: true, unread_count: 1, last: 24, wait_alert: true },
  { id: "c6", protocol: "AT-2019", contact_id: "k8", contact_label: "Caio Ferraz", assignee_id: me, status: "closed", sector_id: "s-suporte", priority: "normal", unread: false, unread_count: 0, last: 990, wait_alert: false },
].map(({ last, ...head }) => ({ ...head, inbox_id: "i1", version: 3, last_message_at: at(last) }));

function message(id: string, direction: "inbound" | "outbound", minutesAgo: number, body: string, extra: Record<string, unknown> = {}) {
  return { id, conversation_id: "c1", direction, body, content_kind: "text", trusted_at: at(minutesAgo), ingested_at: at(minutesAgo), local_state: "", ...extra };
}
const history = {
  head: heads[0],
  messages: [
    message("m1", "inbound", 20, "SIMULAÇÃO · Oi, bom dia! Minha internet está oscilando desde ontem. Trabalho de casa e a conexão caiu durante uma reunião. Conseguem me ajudar?"),
    message("m2", "outbound", 16, "Bom dia, Mariana! Vou verificar agora. Você pode me dizer se as luzes do roteador estão todas acesas?", { local_state: "accepted", receipt: "read" }),
    message("m3", "inbound", 12, "A luz de internet fica piscando em vermelho de vez em quando.", { reply_to_message_id: "m2" }),
    message("m4", "outbound", 7, "Obrigada. Isso indica uma oscilação no sinal. Vou acompanhar a conexão e, se necessário, organizamos uma visita no melhor horário para você.", { local_state: "accepted", receipt: "delivered" }),
    message("m5", "outbound", 6, "Enquanto isso, pode manter o roteador ligado, por favor?", { local_state: "uncertain" }),
    message("m6", "inbound", 4, "Perfeito, fico no aguardo. Obrigada!"),
  ],
  timeline: [
    { id: "e1", kind: "assumed", at: at(18), actor_id: me },
    { id: "e2", kind: "wait_ended", at: at(4), actor_id: "" },
  ],
  next_cursor: 6,
};

const stats = (average: number, median: number, sample: number) => ({ sample, not_applicable: 0, average_seconds: average, median_seconds: median });
const report = {
  timezone: "America/Fortaleza", from: "2026-11-01", to: "2026-11-14", group: "day",
  definitions: ["Primeira resposta humana: da entrada na fila até a primeira mensagem de um atendente.", "Tempo operacional: conta apenas o horário de expediente."],
  conversations: 412, cycles: 438, messages: 6120,
  first_response: stats(252, 160, 398), close_total: stats(11100, 6480, 371), close_operational: stats(4920, 3240, 371), queue_wait: stats(390, 190, 405),
  results: [{ result: "resolved", author_kind: "human", label: "Resolvido", count: 286 }, { result: "no_response", author_kind: "system", label: "Sem resposta", count: 52 }, { result: "forwarded", author_kind: "human", label: "Encaminhado", count: 27 }],
  sectors: [{ sector_id: "s-suporte", name: "Suporte", cycles: 241, queue_waits: 230, queue_wait_median_seconds: 180, responsibility_seconds: 402000 }, { sector_id: "s-financeiro", name: "Financeiro", cycles: 118, queue_waits: 110, queue_wait_median_seconds: 240, responsibility_seconds: 150000 }],
  attendants: [{ membership_id: me, label: "clara.mendes@provedor.example", responsibility_seconds: 210000, messages: 1840, closures: 132 }, { membership_id: "m-rafael", label: "rafael.lima@provedor.example", responsibility_seconds: 190000, messages: 1610, closures: 121 }],
  inboxes: [{ inbox_id: "i1", name: "Atendimento principal", cycles: 351, messages: 5020 }],
  reasons: [{ name: "Instabilidade na conexão", count: 142, overlap: false }, { name: "Segunda via", count: 96, overlap: false }],
  tags: [{ name: "Fibra", count: 198, overlap: true }, { name: "Instabilidade", count: 142, overlap: true }],
  heatmap: [1, 2, 3, 4, 5].flatMap((weekday) => [8, 9, 10, 11, 13, 14, 15, 16, 17, 19].map((hour) => ({ weekday, hour, cycles: Math.round(4 + 18 * Math.exp(-((hour - 10.5) ** 2) / 10) + 9 * Math.exp(-((hour - 15) ** 2) / 6) + (weekday % 3)) }))).concat([{ weekday: 6, hour: 9, cycles: 3 }, { weekday: 6, hour: 10, cycles: 0 }]),
  buckets: [22, 31, 35, 27, 40, 34, 12, 10, 29, 33, 38, 42, 37, 22].map((cycles, index) => ({ key: `2026-11-${String(index + 1).padStart(2, "0")}`, cycles, messages: cycles * 14, closed: Math.round(cycles * 0.85), first_response_sample: cycles - 2, first_response_average_seconds: 240, operational_sample: cycles - 4, operational_average_seconds: 4800 })),
  live: { in_queue: 2, in_attendance: 2, waiting_customer: 1, snoozed: 1, unassigned: 2, online_attendants: 3, longest_queue_wait_seconds: 1440, wait_alerts: 2, wait_alert_seconds: 900 },
  csv: "",
};

const contact = (id: string, name: string, phone: string, blocked = false) => ({ id, name, phone, email: null, notes: "", blocked, block_reason: blocked ? "SIMULAÇÃO · Pedido do titular" : "", avatar_reference: null, last_contact: null, created_at: at(9000), updated_at: at(60) });
const contacts = [
  contact("k5", "Beatriz Siqueira", "+55 85 90000-0105"), contact("k8", "Caio Ferraz", "+55 85 90000-0108", true), contact("k3", "Helena Prado", "+55 85 90000-0103"),
  contact("k7", "Joana Farias", "+55 85 90000-0107"), contact("k4", "Lucas Almeida", "+55 85 90000-0104"), contact("k1", "Mariana Costa", "+55 85 90000-0101"),
  contact("k6", "Pedro Martins", "+55 85 90000-0106"), contact("k2", "Rafael Nogueira", "+55 85 90000-0102"),
];

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function respond(path: string, method: string): Response {
  if (signedOut) return json({ detail: "Sem sessão" }, 401);
  if (path.endsWith("/branding")) return json({ display_name: "Provedor Demonstração", fallback_name: "Chat ISP", has_logo: false });
  if (path.endsWith("/presence/heartbeat") || path.endsWith("/presence/manual")) return json({ manual: "available", connected: true });
  if (path.endsWith("/presence/occupancy")) return json({ members: [{ membership_id: me, occupied: 3, capacity: 6, open: 2, waiting: 1, snoozed: 1 }] });
  if (path.endsWith("/inboxes")) return json({ inboxes: [{ id: "i1", name: "Atendimento principal", channel_status: "ready", connector_type: "meta_cloud" }] });
  if (path.endsWith("/contacts")) return json({ items: contacts, next_offset: null });
  if (/\/contacts\/[^/]+\/history$/.test(path)) return json({ items: [{ id: "c1", protocol: "AT-2048", status: "open" }, { id: "c0", protocol: "AT-1870", status: "closed" }], next_offset: null });
  if (/\/contacts\/[^/]+\/document$/.test(path)) return json({ present: true, kind: "cpf", version: 1 });
  if (path.endsWith("/history")) return json(history);
  if (path.endsWith("/queue")) {
    return json({ items: heads, counts: { mine: heads.filter((h) => h.assignee_id === me).length, unassigned: heads.filter((h) => !h.assignee_id).length, all: heads.length }, page_cursor: "" });
  }
  if (path.endsWith("/conversations")) return json({ conversations: heads, messages: [], next_cursor: 0 });
  if (path.endsWith("/events")) return json({ events: [], next_cursor: 0 });
  if (path.endsWith("/directory")) return json({ items: people, next_cursor: null });
  if (path.endsWith("/sectors")) return json({ sectors });
  if (path.endsWith("/annotation-tags")) return json([{ id: "t1", name: "Fibra" }, { id: "t2", name: "Instabilidade" }]);
  if (path.endsWith("/annotations")) return json({ priority: "urgent", tags: [{ id: "t1", name: "Fibra" }], watermark: 0, read_version: 0, notes: [{ id: "n1", author_id: me, body: "SIMULAÇÃO · Sinal óptico abaixo do esperado. Verificar conector e avaliar visita.", created_at: at(12) }], events: [] });
  if (path.includes("/preferences/notifications") || path.endsWith("/notifications")) return json({ sound: false, browser: false, silent: true });
  if (path.endsWith("/attendant-signature")) return json({ enabled: false, prefix: "", version: 1 });
  if (path.includes("/reports")) return json(report);
  if (path.endsWith("/packages")) return json({ packages: [] });
  if (method !== "GET") return json({ detail: "Bancada visual: nenhuma alteração é gravada." }, 409);
  // Sem resposta sintética para esta rota: a tela mostra o próprio estado de indisponível.
  return json({ type: "urn:chatisp:problem:bancada", title: "Indisponível", status: 404, detail: "Bancada visual: rota sem dados sintéticos." }, 404);
}

window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const request = input instanceof Request ? input : new Request(input, init);
  return respond(new URL(request.url, window.location.origin).pathname, request.method);
};
// Sem WebSocket: o painel cai no modo de atualização manual, como quando o tempo real está indisponível.
Reflect.deleteProperty(window, "WebSocket");

if (params.get("tema") === "light" || params.get("tema") === "dark") {
  if (useInterfaceStore.getState().theme !== params.get("tema")) useInterfaceStore.setState({ theme: params.get("tema") as "light" | "dark" });
}
if (params.get("menu")) useInterfaceStore.setState({ menuExpanded: params.get("menu") === "expanded" });
syncInterfaceToDocument();

if (!signedOut) {
  useSessionStore.getState().applySession(
    { tenantId: "t-demo", userId: "u-clara", sessionId: "s-bancada", email: "clara.mendes@provedor.example", membershipId: me, role, expiresAt: "2099-01-01T00:00:00Z", mfaEnrollmentRequired: false, mfaEnabled: false },
    "csrf-bancada",
  );
}

// ?conversa=AT-2048 abre o atendimento com esse protocolo; ?detalhes=2 abre o grupo de detalhes de número 2;
// ?acao=Adiar clica no botão com esse texto dentro dos detalhes; ?filtros=1 abre os filtros da fila.
const openProtocol = params.get("conversa");
if (openProtocol) {
  const timer = window.setInterval(() => {
    const item = [...document.querySelectorAll<HTMLButtonElement>(".queue-item")].find((button) => button.textContent?.includes(openProtocol));
    if (!item) return;
    window.clearInterval(timer);
    item.click();
    const group = Number(params.get("detalhes") ?? 0);
    if (group > 0) window.setTimeout(() => document.querySelectorAll<HTMLElement>('.context-tabs [role="tab"]')[group - 1]?.click(), 400);
    const action = params.get("acao");
    if (action) window.setTimeout(() => [...document.querySelectorAll<HTMLButtonElement>(".workspace-context button")].find((button) => button.textContent?.trim() === action)?.click(), 900);
  }, 150);
}
// ?contato=Mariana abre a ficha do contato cujo nome contém esse texto.
if (params.get("contato")) {
  const timer = window.setInterval(() => {
    const row = [...document.querySelectorAll<HTMLButtonElement>(".split-rows button")].find((button) => button.textContent?.includes(params.get("contato") ?? ""));
    if (!row) return;
    window.clearInterval(timer);
    row.click();
  }, 150);
}
if (params.get("filtros")) {
  const timer = window.setInterval(() => {
    const summary = document.querySelector<HTMLElement>(".queue-filters > summary");
    if (!summary) return;
    window.clearInterval(timer);
    summary.click();
  }, 150);
}

const root = document.getElementById("root");
if (!root) throw new Error("Elemento raiz ausente.");
createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={createQueryClient()}>
      <SessionQuerySync />
      <MemoryRouter initialEntries={[route]}>
        <AppRoutes />
      </MemoryRouter>
    </QueryClientProvider>
  </StrictMode>,
);
