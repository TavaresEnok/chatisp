import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { TeamsPage } from "@/pages/teams-page";
import { ContactsPage } from "@/pages/contacts-page";
import { ConversationsPage } from "@/pages/conversations-page";
import { MessageComposer } from "@/features/conversation/message-composer";
import { NotificationPreferences } from "@/features/notifications/notification-preferences";
import { useNotificationEvents, type NotificationBatch } from "@/features/notifications/use-notification-events";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";

afterEach(() => vi.unstubAllGlobals());
const principal: PanelPrincipal = { tenantId: "tenant", userId: "user", sessionId: "session", membershipId: "member", role: "admin", email: "a@example.test", expiresAt: "2099-01-01", mfaEnrollmentRequired: false };
function session(p = principal) { act(() => useSessionStore.getState().applySession(p, "csrf")); }
function mount(element: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><MemoryRouter><Routes><Route path="/" element={element} /><Route path="/entrar" element={<p>Sessão encerrada</p>} /></Routes></MemoryRouter></QueryClientProvider>);
  return { ...view, client };
}
function json(value: unknown, status = 200) { return new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } }); }

it("admin cadastra setor e contato; troca para agente limpa edição e administração", async () => {
  const calls: { method: string; path: string; body: unknown }[] = [];
  let created = false;
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => {
    const path = new URL(req.url).pathname;
    const body = req.method === "POST" ? await req.json() : null;
    calls.push({ method: req.method, path, body });
    if (path === "/api/panel/sectors" && req.method === "POST") { created = true; return json({ id: "s1", name: "Suporte", active: true }, 201); }
    if (path === "/api/panel/sectors") return json({ sectors: created ? [{ id: "s1", name: "Suporte", active: true }] : [] });
    if (path.endsWith("/sectors/s1")) return json({ membership_ids: [], inbox_ids: [], default_inbox_ids: [] });
    if (path === "/api/panel/contacts" && req.method === "POST") return json({ id: "p1", ...(body as object) }, 201);
    return json({ items: [], next_offset: null });
  }));
  session(); const user = userEvent.setup();
  const view = mount(<><TeamsPage /><ContactsPage /></>);
  await user.type(await screen.findByLabelText("Nome do setor"), "Suporte");
  await user.click(screen.getByRole("button", { name: "Criar setor" }));
  expect(await screen.findByRole("button", { name: "Suporte — Ativo" })).toBeInTheDocument();
  await user.type(screen.getByLabelText("Nome", { exact: true }), "Pessoa");
  await user.type(screen.getByLabelText("Telefone internacional"), "+5511988887777");
  await user.type(screen.getByLabelText("Observações"), "Observação autorizada");
  await user.click(screen.getByRole("button", { name: "Salvar contato" }));
  await waitFor(() => expect(calls.find(c => c.path.endsWith("/contacts") && c.method === "POST")?.body).toMatchObject({ name: "Pessoa", phone: "+5511988887777", notes: "Observação autorizada" }));
  await user.type(screen.getByLabelText("Nome", { exact: true }), "Edição privada");
  const previousScope = JSON.stringify([principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId]);
  expect(view.client.getQueryCache().findAll({ queryKey: ["m3", previousScope] }).length).toBeGreaterThan(0);
  session({ ...principal, userId: "outro", sessionId: "outra", membershipId: "outro", role: "agent" });
  await waitFor(() => expect(screen.queryByRole("button", { name: "Criar setor" })).not.toBeInTheDocument());
  expect(screen.getByLabelText("Nome", { exact: true })).toHaveValue("");
  expect(view.client.getQueryCache().findAll({ queryKey: ["m3", previousScope] })).toHaveLength(0);
}, 15000);

it("rascunho e formatação sobrevivem à troca de conversa e somem na troca de usuário", async () => {
  session({ ...principal, role: "agent" });
  const user = userEvent.setup(); const send = vi.fn();
  const a = { tenantId: "tenant", userId: "user", conversationId: "c1" };
  const view = render(<MessageComposer scope={a} onSend={send} />);
  await user.type(screen.getByLabelText("Mensagem"), "Rascunho");
  await user.click(screen.getByRole("button", { name: "Inserir 👍" }));
  expect(screen.getByLabelText("Mensagem")).toHaveValue("Rascunho👍");
  view.rerender(<MessageComposer scope={{ ...a, conversationId: "c2" }} onSend={send} />);
  expect(screen.getByLabelText("Mensagem")).toHaveValue("");
  view.rerender(<MessageComposer scope={a} onSend={send} />);
  expect(screen.getByLabelText("Mensagem")).toHaveValue("Rascunho👍");
  session({ ...principal, role: "agent", userId: "user2", sessionId: "session2" });
  view.rerender(<MessageComposer scope={{ ...a, userId: "user2" }} onSend={send} />);
  expect(screen.getByLabelText("Mensagem")).toHaveValue("");
  expect(localStorage.length).toBe(0);
  expect(send).not.toHaveBeenCalled();
});

it("solicita permissão por gesto explícito e descarta lote da sessão anterior", async () => {
  const shown = vi.fn(); const permission = vi.fn(async () => "granted" as const);
  class BrowserNotification {
    static permission: NotificationPermission = "default";
    static requestPermission = permission;
    constructor(title: string) { shown(title); }
  }
  vi.stubGlobal("Notification", BrowserNotification);
  const prefs = { sound: false, browser: true, silent: false };
  const notify = vi.fn(); const save = vi.fn(async () => {});
  const batch: NotificationBatch = { scope: "session-a", events: [{ id: "e1", cursor: 1, kind: "new_message", live: true }] };
  function Harness({ scope, incoming }: { scope: string; incoming: NotificationBatch | null }) {
    useNotificationEvents(scope, prefs, incoming, notify);
    return <NotificationPreferences key={scope} prefs={prefs} onSave={save} />;
  }
  const view = render(<Harness scope="session-a" incoming={null} />);
  expect(permission).not.toHaveBeenCalled();
  await userEvent.setup().click(screen.getByRole("button", { name: "Permitir notificações" }));
  expect(permission).toHaveBeenCalledTimes(1);
  BrowserNotification.permission = "granted";
  view.rerender(<Harness scope="session-a" incoming={batch} />);
  expect(shown).toHaveBeenCalledExactlyOnceWith("Nova mensagem");
  view.rerender(<Harness scope="session-b" incoming={batch} />);
  expect(shown).toHaveBeenCalledTimes(1);
  expect(notify).toHaveBeenCalledTimes(1);
  view.rerender(<Harness scope="session-b" incoming={{ ...batch, scope: "session-b" }} />);
  expect(shown).toHaveBeenCalledTimes(2);
});

it("revogação operacional pelo socket limpa conversa, histórico, cache e rascunho", async () => {
  class Socket {
    static current: Socket;
    onopen: (() => void) | null = null;
    onmessage: ((e: { data: string }) => void) | null = null;
    onclose: ((e: { code: number; reason: string }) => void) | null = null;
    send = vi.fn(); close = vi.fn();
    constructor() { Socket.current = this; }
  }
  vi.stubGlobal("WebSocket", Socket);
  const head = { id: "c1", protocol: "M3A-SUPORTE", contact_id: "p1", assignee_id: "", summary: "Atendimento", status: "open", version: 2 };
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => {
    const path = new URL(req.url).pathname;
    if (path.endsWith("/inboxes")) return json({ inboxes: [{ id: "i1", name: "Caixa", channel_status: "connected" }] });
    if (path.endsWith("/preferences/notifications")) return json({ sound: false, browser: false, silent: true });
    if (path.endsWith("/contacts/p1/history")) return json({ items: [{ id: "old", protocol: "M3A-ANTERIOR", status: "closed" }], next_offset: null });
    if (path.endsWith("/history")) return json({ head, messages: [], timeline: [], next_cursor: 1 });
    return json({ conversations: [head], messages: [], next_cursor: 1 });
  }));
  session({ ...principal, role: "agent" }); const user = userEvent.setup();
  const view = mount(<ConversationsPage />);
  await user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
  await user.click(await screen.findByRole("option", { name: /M3A-SUPORTE/ }));
  expect(await screen.findByText(/M3A-ANTERIOR/)).toBeInTheDocument();
  await user.type(screen.getByLabelText("Mensagem"), "Privado");
  expect(localStorage.length).toBeGreaterThan(0);
  act(() => Socket.current.onclose?.({ code: 1008, reason: "authorization_revoked" }));
  expect(await screen.findByText("Sessão encerrada")).toBeInTheDocument();
  expect(screen.queryByText(/M3A-ANTERIOR/)).not.toBeInTheDocument();
  expect(screen.queryByLabelText("Mensagem")).not.toBeInTheDocument();
  expect(localStorage.length).toBe(0);
  expect(view.client.getQueryCache().getAll()).toHaveLength(0);
});
