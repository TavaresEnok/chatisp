import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { ConversationsPage } from "@/pages/conversations-page";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
import { draftKey, readDraft } from "@/features/conversation/draft-store";

const principal = { tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role: "agent", email: "a@b.test", expiresAt: "2099-01-01", mfaEnrollmentRequired: false };
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
afterEach(() => { resetSessionStore(); localStorage.clear(); vi.unstubAllGlobals(); });

function setup(deferred = false) {
  const sockets: Socket[] = [];
  class Socket {
    onopen?: () => void;
    onmessage?: (event: { data: string }) => void;
    onclose?: (event: { code: number; reason: string }) => void;
    send = vi.fn(); close = vi.fn();
    constructor() { sockets.push(this); }
  }
  vi.stubGlobal("WebSocket", Socket);
  let transferred = false;
  let finishTransfer!: () => void;
  const sends: unknown[] = [];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const url = new URL(request.url), path = url.pathname;
    if (path === "/api/panel/session") return response({ tenant_id: "t", user_id: "u", session_id: "s", membership_id: "m", role: "agent" });
    if (path.endsWith("/transfer-sectors")) return response([{ id: "s2", name: "Destino fora dos vínculos" }]);
    if (path.endsWith("/transfer-sector")) {
      expect(await request.json()).toMatchObject({ target_id: "s2", expected_version: 3 });
      if (deferred) await new Promise<void>(resolve => { finishTransfer = resolve; });
      transferred = true; return response({ id: "c1" });
    }
    if (path.endsWith("/messages")) {
      sends.push(await request.json());
      if (sends.length === 1) throw new TypeError("resposta perdida");
      return response({ state: "queued" });
    }
    if (path.endsWith("/inboxes")) return response({ inboxes: [{ id: "i1", name: "Origem", channel_status: "connected" }, { id: "i2", name: "Outra", channel_status: "connected" }] });
    const heads = [{ id: "c1", protocol: "P1", status: "open", version: 3 }, { id: "c2", protocol: "P2", status: "open", version: 7 }];
    const otherInbox = path.includes("/i2/") || url.searchParams.get("inbox") === "i2";
    const allowed = transferred || otherInbox ? heads.slice(1) : heads;
    if (path.endsWith("/queue")) return response({ items: allowed.map(head => ({ ...head, inbox_id: path.includes("/i2/") ? "i2" : "i1", contact_label: head.id, sector_id: "s1", priority: "normal", unread: false })), counts: { mine: allowed.length, all: allowed.length, unassigned: 0 } });
    if (path.endsWith("/history")) {
      const id = path.includes("/c1/") ? "c1" : "c2";
      if (transferred && id === "c1") return response({ detail: "conversa fora do escopo" }, 403);
      return response({ head: heads.find(head => head.id === id), messages: [], timeline: [], next_cursor: 0 });
    }
    if (path.endsWith("/conversations")) return response({ conversations: allowed, messages: [], next_cursor: 0 });
    if (path.endsWith("/directory")) return response({ items: [], next_cursor: null });
    if (path.endsWith("/sectors")) return response({ sectors: [] });
    if (path.endsWith("/annotation-tags")) return response([]);
    if (path.endsWith("/annotations")) return response({ priority: "normal", tags: [], notes: [], events: [] });
    // The composer requires a complete signature contract before any send.
    if (path === "/api/panel/attendant-signature") return response({ enabled: false, prefix: "", version: 1 });
    return response({});
  }));
  useSessionStore.getState().applySession(principal, "csrf");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><MemoryRouter><ConversationsPage /></MemoryRouter></QueryClientProvider>);
  const user = userEvent.setup();
  async function select(id: string) {
    await user.click(await screen.findByRole("button", { name: new RegExp(`Protocolo ${id}`) }));
    return screen.findByRole("textbox", { name: "Mensagem" });
  }
  async function transfer() {
    await user.click(screen.getByRole("button", { name: "Transferir para setor" }));
    await screen.findByRole("option", { name: "Destino fora dos vínculos" });
    await user.selectOptions(screen.getByLabelText("Setor", { selector: 'div[role="dialog"] select' }), "s2");
    await user.click(screen.getByRole("button", { name: "Confirmar" }));
  }
  return { ...view, client, sockets, user, select, transfer, sends, finish: () => finishTransfer() };
}

it("transferência com socket ativo preserva sessão, outro rascunho e a intenção perdida", async () => {
  const h = setup();
  await h.user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
  act(() => { h.sockets[0]?.onopen?.(); h.sockets[0]?.onmessage?.({ data: JSON.stringify({ type: "snapshot", inbox_id: "i1" }) }); });
  await h.user.type(await h.select("P2"), "Pendente");
  await h.user.click(screen.getByRole("button", { name: "Enviar" }));
  await screen.findByText(/resposta do envio não chegou/i);
  const draft = draftKey(principal, { tenantId: "t", userId: "u:s", conversationId: "c2" });
  const saved = readDraft(draft);
  expect(saved?.pending).toMatchObject({ text: "Pendente" });
  await h.select("P1"); await h.transfer();
  await waitFor(() => expect(screen.queryByRole("button", { name: /Protocolo P1/ })).not.toBeInTheDocument());
  act(() => h.sockets[0]?.onclose?.({ code: 1008, reason: "authorization_revoked" }));
  expect(screen.queryByLabelText("Mensagem")).not.toBeInTheDocument();
  await waitFor(() => expect(h.sockets).toHaveLength(2));
  act(() => { h.sockets[1]?.onopen?.(); h.sockets[1]?.onmessage?.({ data: JSON.stringify({ type: "snapshot", inbox_id: "i1" }) }); });
  expect(await h.select("P2")).toHaveValue(saved?.draft);
  expect(useSessionStore.getState().principal).toEqual(principal);
  expect(readDraft(draft)?.pending).toEqual(saved?.pending);
  expect(h.client.getQueryData(["attendance", "t", "u", "s", "agent", "m", "i1", "history", "c1"])).toBeUndefined();
  expect(screen.queryByRole("button", { name: /Protocolo P1/ })).not.toBeInTheDocument();
  await h.user.click(screen.getByRole("button", { name: "Enviar" }));
  await waitFor(() => expect(h.sends).toHaveLength(2));
  expect(h.sends[1]).toEqual(h.sends[0]);
  expect(h.sockets).toHaveLength(2);
  h.unmount(); h.client.clear();
}, 15000);

it.each(["conversation", "inbox"])("transferência tardia após mudança de %s preserva seleção e controles", async change => {
  const h = setup(true);
  await h.user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
  await h.select("P1"); await h.transfer();
  if (change === "inbox") await h.user.selectOptions(screen.getAllByLabelText("Caixa")[0]!, "i2");
  const composer = await h.select("P2");
  await h.user.type(composer, "Rascunho atual");
  await act(async () => h.finish());
  await waitFor(() => expect(screen.queryByRole("button", { name: /Protocolo P1/ })).not.toBeInTheDocument());
  expect(screen.getByRole("textbox", { name: "Mensagem" })).toBe(composer);
  expect(composer).toHaveValue("Rascunho atual");
  expect(screen.getAllByLabelText("Caixa")[0]).toHaveValue(change === "inbox" ? "i2" : "i1");
  h.unmount(); h.client.clear();
});
