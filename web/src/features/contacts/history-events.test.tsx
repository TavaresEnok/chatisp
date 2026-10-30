import { act, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { ContactHistory } from "@/features/contacts/contact-history";
import { useAttendanceEvents } from "@/features/conversation/use-attendance-events";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
import { useState } from "react";
import type { NotificationBatch } from "@/features/notifications/use-notification-events";
import { useNotificationEvents } from "@/features/notifications/use-notification-events";
import { useAttendance } from "@/features/conversation/use-attendance";
import { fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { ConversationsPage } from "@/pages/conversations-page";
import { draftKey, readDraft, writeDraft } from "@/features/conversation/draft-store";
afterEach(() => { resetSessionStore(); vi.unstubAllGlobals(); localStorage.clear(); });
it("preserva rascunho na navegação e remove texto e intenção ao trocar perfil ou vínculo", () => {
  const principal = { tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "supervisor", expiresAt: "2099-01-01", mfaEnrollmentRequired: false };
  const scope = { tenantId: "t", userId: "u:s", conversationId: "c" }; const key = draftKey(principal, scope);
  const state = { draft: "texto privado", pending: { intentKey: "intencao-antiga", text: "texto privado" }, phase: "uncertain" as const, notice: null };
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ inboxes: [] }), { headers: { "content-type": "application/json" } })));
  useSessionStore.getState().applySession(principal, "csrf"); writeDraft(key, state);
  const mount = () => render(<QueryClientProvider client={new QueryClient()}><MemoryRouter><ConversationsPage /></MemoryRouter></QueryClientProvider>);
  const first = mount(); first.unmount(); const second = mount(); expect(readDraft(key)?.pending?.intentKey).toBe("intencao-antiga"); second.unmount();
  act(() => useSessionStore.getState().applySession({ ...principal, role: "agent" }, "csrf")); expect(readDraft(key)).toBeUndefined();
  act(() => useSessionStore.getState().applySession(principal, "csrf")); expect(readDraft(key)).toBeUndefined();
  writeDraft(key, state); act(() => useSessionStore.getState().applySession({ ...principal, membershipId: "outro" }, "csrf")); expect(readDraft(key)).toBeUndefined();
  act(() => useSessionStore.getState().applySession(principal, "csrf")); expect(readDraft(key)).toBeUndefined();
});
it("carrega apenas histórico autorizado do contato e oculta erro do servidor", async () => {
  const fetch = vi.fn(async (req: Request) => { expect(new URL(req.url).pathname).toBe("/api/panel/contacts/c1/history"); return new Response(JSON.stringify({ detail: "contato secreto" }), { status: 404, headers: { "content-type": "application/problem+json" } }); }); vi.stubGlobal("fetch", fetch);
  render(<QueryClientProvider client={new QueryClient()}><ContactHistory contactId="c1" scope={["t", "u", "s", "agent", "m"]} /></QueryClientProvider>);
  expect(await screen.findByRole("alert")).toHaveTextContent("histórico autorizado"); expect(document.body.textContent).not.toContain("contato secreto");
});
it("atualiza histórico nos eventos reais e remove dados do supervisor ao trocar perfil", async () => {
  const sockets: FakeSocket[] = [];
  class FakeSocket { static OPEN = 1; readyState = 1; onopen: (() => void) | null = null; onmessage: ((event: { data: string }) => void) | null = null; onclose: ((event: { code: number; reason: string }) => void) | null = null; send = vi.fn(); close = vi.fn(); constructor() { sockets.push(this); } }
  vi.stubGlobal("WebSocket", FakeSocket);
  let status = "open"; let added = false; let historyCalls = 0;
  const head = { id: "c1", protocol: "PRIVADO-123", contact_id: "cliente", assignee_id: "operador", status: "open", version: 3 };
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => {
    const path = new URL(req.url).pathname; let data: unknown = {};
    if (path.endsWith("/inboxes")) data = { inboxes: [{ id: "i1", name: "Caixa", channel_status: "connected" }] };
    else if (path.includes("/contacts/") && path.endsWith("/history")) { historyCalls++; data = { items: [{ id: "c1", protocol: "PRIVADO-123", status }, ...(added ? [{ id: "c2", protocol: "NOVA-456", status: "open" }] : [])], next_offset: null }; }
    else if (path.endsWith("/history")) data = { head, messages: [], timeline: [], next_cursor: 1 };
    else if (path.endsWith("/conversations")) data = { conversations: [head], messages: [], next_cursor: 1 };
    return new Response(JSON.stringify(data), { headers: { "content-type": "application/json" } });
  }));
  const principal = { tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "supervisor", expiresAt: "2099-01-01", mfaEnrollmentRequired: false };
  useSessionStore.getState().applySession(principal, "csrf");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><MemoryRouter><ConversationsPage /></MemoryRouter></QueryClientProvider>);
  fireEvent.change(await screen.findByLabelText("Caixa"), { target: { value: "i1" } });
  fireEvent.click(await screen.findByRole("option", { name: /Protocolo PRIVADO-123/ }));
  expect(await screen.findByText("PRIVADO-123 — Aberta")).toBeInTheDocument();
  const socket = sockets[0]!; const send = (id: string, type: string) => act(() => socket.onmessage?.({ data: JSON.stringify({ type: "events", inbox_id: "i1", events: [{ ID: id, Seq: Number(id), Type: type }] }) }));
  status = "closed"; send("1", "conversation.closed"); await screen.findByText("PRIVADO-123 — Encerrada");
  status = "waiting_customer"; send("2", "conversation.reopened"); await screen.findByText("PRIVADO-123 — Aguardando cliente");
  status = "future_private_enum"; added = true; send("3", "conversation.opened"); await screen.findByText("PRIVADO-123 — Status desconhecido"); await screen.findByText("NOVA-456 — Aberta"); expect(document.body.textContent).not.toContain("future_private_enum"); expect(historyCalls).toBeGreaterThanOrEqual(4);
  act(() => useSessionStore.getState().applySession({ ...principal, role: "agent", membershipId: "outro" }, "csrf"));
  await waitFor(() => expect(client.getQueryCache().findAll().some(q => JSON.stringify(q.state.data)?.includes("PRIVADO-123"))).toBe(false));
});
it("não reproduz lote antigo ao trocar caixa e não sinaliza mensagem histórica", async () => {
  const sockets: FakeSocket[] = [];
  class FakeSocket { static OPEN = 1; readyState = 1; onopen: (() => void) | null = null; onmessage: ((event: { data: string }) => void) | null = null; onclose: ((event: { code: number; reason: string }) => void) | null = null; send = vi.fn(); close = vi.fn(); constructor() { sockets.push(this); } }
  vi.stubGlobal("WebSocket", FakeSocket); vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ inboxes: [], conversations: [] }), { headers: { "content-type": "application/json" } })));
  const signal = vi.fn(); const principal = { tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false };
  function Probe() { const a = useAttendance(principal); useNotificationEvents(a.inbox ? `t:u:s:${a.inbox}` : null, { sound: false, browser: false, silent: false }, a.notificationBatch, signal); return <><button onClick={() => a.selectInbox("a")}>A</button><button onClick={() => a.selectInbox("b")}>B</button><button onClick={() => a.selectInbox("")}>Limpar</button></>; }
  render(<QueryClientProvider client={new QueryClient()}><Probe /></QueryClientProvider>);
  const send = (socket: FakeSocket, message: unknown) => act(() => socket.onmessage?.({ data: JSON.stringify(message) }));
  fireEvent.click(screen.getByText("A")); const first = sockets[0]!; act(() => first.onopen?.()); send(first, { type: "snapshot", inbox_id: "a" });
  send(first, { type: "events", inbox_id: "a", events: [{ ID: "historical", Seq: 1, Type: "conversation.message", Payload: { historical: true } }, { ID: "current", Seq: 2, Type: "conversation.message", Payload: { historical: false } }] }); expect(signal).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByText("Limpar")); send(first, { type: "events", inbox_id: "a", events: [{ ID: "late", Seq: 3, Type: "conversation.message" }] }); fireEvent.click(screen.getByText("A")); expect(signal).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByText("B")); const b = sockets.at(-1)!; send(b, { type: "snapshot", inbox_id: "b" }); fireEvent.click(screen.getByText("A")); expect(signal).toHaveBeenCalledTimes(1);
});
it("eventos anteriores ao snapshot não são ao vivo e revogação limpa sessão e cache", async () => {
  const sockets: FakeSocket[] = [];
  class FakeSocket { onopen: (() => void) | null = null; onmessage: ((event: { data: string }) => void) | null = null; onclose: ((event: { code: number; reason: string }) => void) | null = null; send = vi.fn(); close = vi.fn(); constructor() { sockets.push(this); } }
  vi.stubGlobal("WebSocket", FakeSocket);
  const client = new QueryClient(); client.setQueryData(["private"], "segredo"); localStorage.setItem("draft", "segredo");
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  function Probe() { const [batch, setBatch] = useState<NotificationBatch | null>(null); useAttendanceEvents("i1", ["attendance", "t", "u", "s", "i1"], setBatch); return <span>{JSON.stringify(batch)}</span>; }
  render(<QueryClientProvider client={client}><Probe /></QueryClientProvider>);
  act(() => { sockets[0]?.onopen?.(); sockets[0]?.onmessage?.({ data: JSON.stringify({ type: "events", inbox_id: "i1", events: [{ ID: "e1", Seq: 1, Type: "conversation.message" }] }) }); });
  expect(screen.getByText(/"live":false/)).toBeInTheDocument();
  act(() => { sockets[0]?.onmessage?.({ data: JSON.stringify({ type: "snapshot", inbox_id: "i1", next_cursor: 1 }) }); sockets[0]?.onmessage?.({ data: JSON.stringify({ type: "events", inbox_id: "i1", events: [{ ID: "e2", Seq: 2, Type: "conversation.opened" }] }) }); });
  expect(screen.getByText(/"live":true/)).toBeInTheDocument();
  act(() => sockets[0]?.onclose?.({ code: 1008, reason: "authorization_lost" })); await waitFor(() => expect(useSessionStore.getState().principal).toBeNull()); expect(client.getQueryData(["private"])).toBeUndefined(); expect(localStorage.getItem("draft")).toBeNull();
});
