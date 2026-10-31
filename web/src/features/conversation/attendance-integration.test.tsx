import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { ConversationsPage } from "@/pages/conversations-page";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
import { setSessionLossHandler } from "@/api/client";
import { useAttendanceEvents } from "@/features/conversation/use-attendance-events";
import { useAttendance } from "@/features/conversation/use-attendance";

afterEach(() => { resetSessionStore(); setSessionLossHandler(null); vi.unstubAllGlobals(); });
it.each(["slow_client", "resync_required"])("mantém cache e atendimento degradado após 1008 %s sem reconexão", async reason => {
  const sockets: Socket[] = [];
  class Socket { onclose?: (event: { code: number; reason: string }) => void; send = vi.fn(); close = vi.fn(); constructor() { sockets.push(this); } }
  vi.stubGlobal("WebSocket", Socket);
  const qc = new QueryClient();
  const key = ["attendance", "t", "u", "s", "agent", "m", "i1"];
  qc.setQueryData([...key, "history", "c"], { body: "preservado" });
  const { result, unmount } = renderHook(() => useAttendanceEvents("i1", key), { wrapper: ({ children }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider> });
  act(() => sockets[0]?.onclose?.({ code: 1008, reason }));
  expect(result.current).toBe("degraded");
  expect(qc.getQueryData([...key, "history", "c"])).toEqual({ body: "preservado" });
  expect(sockets).toHaveLength(1);
  unmount(); qc.clear();
});

it.each([403, 404])("limpa diretório após negativa HTTP %s com fila vazia e sem socket", async status => {
  vi.stubGlobal("WebSocket", undefined);
  let denied = false, directoryCalls = 0;
  const label = "operator@example.com";
  const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (path.endsWith("/directory")) { directoryCalls++; return denied ? response({ detail: "negado" }, status) : response({ items: [{ membership_id: "outside-page", label, role: "agent" }], next_cursor: null }); }
    if (path.endsWith("/inboxes")) return response({ inboxes: [{ id: "i1", name: "Principal", channel_status: "degraded" }] });
    if (path.endsWith("/queue")) return response({ items: [], counts: { mine: 0, all: 0, unassigned: 0 } });
    if (path.endsWith("/conversations")) return response({ conversations: [], messages: [], next_cursor: 0 });
    if (path.endsWith("/sectors")) return response({ sectors: [] });
    if (path.endsWith("/annotation-tags")) return response([]);
    return response({});
  }));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const key = ["m3b-directory", "t", "u", "s", "agent", "m", "i1"];
  const user = userEvent.setup();
  const view = render(<QueryClientProvider client={qc}><MemoryRouter><ConversationsPage /></MemoryRouter></QueryClientProvider>);
  await user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
  expect(await screen.findByRole("option", { name: label })).toHaveValue("outside-page");
  denied = true;
  await act(async () => { await qc.invalidateQueries({ queryKey: key }); });
  await waitFor(() => expect(qc.getQueryData(key)).toBeUndefined());
  expect(screen.queryByRole("option", { name: label })).not.toBeInTheDocument();
  const calls = directoryCalls;
  await act(async () => { await qc.invalidateQueries({ queryKey: key }); });
  expect(directoryCalls).toBe(calls);
  expect(screen.queryByText(/Acesso à caixa encerrado/)).not.toBeInTheDocument();
  view.unmount(); qc.clear();
});

it("transfere para setor fora dos vínculos e remove o conteúdo da conversa", async () => {
  vi.stubGlobal("WebSocket", undefined);
  let transferred = false;
  const response = (body: unknown) => new Response(JSON.stringify(body), { headers: { "content-type": "application/json" } });
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    if (path.endsWith("/transfer-sectors")) return response([{ id: "s2", name: "Destino permitido" }]);
    if (path.endsWith("/transfer-sector")) { expect(await request.json()).toMatchObject({ target_id: "s2", expected_version: 3 }); transferred = true; return response({ id: "c1" }); }
    if (path.endsWith("/inboxes")) return response({ inboxes: [{ id: "i1", name: "Principal", channel_status: "degraded" }] });
    const head = { id: "c1", protocol: "P1", status: "open", version: 3 };
    if (path.endsWith("/queue")) return response({ items: transferred ? [] : [{ ...head, inbox_id: "i1", contact_label: "Cliente", sector_id: "s1", priority: "normal", unread: false }], counts: { mine: 1, all: 1, unassigned: 0 } });
    if (path.endsWith("/history")) return response({ head, messages: [], timeline: [], next_cursor: 0 });
    if (path.endsWith("/conversations")) return response({ conversations: transferred ? [] : [head], messages: [], next_cursor: 0 });
    if (path.endsWith("/directory")) return response({ items: [], next_cursor: null });
    if (path.endsWith("/sectors")) return response({ sectors: [{ id: "s1", name: "Origem", active: true }] });
    if (path.endsWith("/annotation-tags")) return response([]);
    if (path.endsWith("/annotations")) return response({ priority: "normal", tags: [], notes: [], events: [] });
    return response({});
  }));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const user = userEvent.setup();
  const view = render(<QueryClientProvider client={qc}><MemoryRouter><ConversationsPage /></MemoryRouter></QueryClientProvider>);
  await user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
  await user.click(await screen.findByRole("button", { name: /Protocolo P1/ }));
  await screen.findByLabelText("Mensagem");
  await user.click(await screen.findByRole("button", { name: "Transferir para setor" }));
  await screen.findByRole("option", { name: "Destino permitido" });
  await user.selectOptions(await screen.findByLabelText("Setor", { selector: 'div[role="dialog"] select' }), "s2");
  await user.click(screen.getByRole("button", { name: "Confirmar" }));
  await waitFor(() => expect(screen.queryByLabelText("Mensagem")).not.toBeInTheDocument());
  expect(transferred).toBe(true);
  expect(qc.getQueryData(["attendance", "t", "u", "s", "agent", "m", "i1", "history", "c1"])).toBeUndefined();
  view.unmount(); qc.clear();
});
it("carrega caixa e histórico e envia comando versionado pelo contrato HTTP", async () => {
  const requests: { path: string; body: unknown }[] = [];
  const head = { id: "c1", protocol: "P1", contact_id: "Cliente", assignee_id: "Operador", status: "open", version: 3 };
  vi.stubGlobal("WebSocket", undefined);
  let sends = 0;
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    const body = request.method === "POST" ? await request.json() : null;
    requests.push({ path, body });
    if (path.endsWith("/messages") && ++sends === 1) throw new TypeError("rede");
    let data: unknown = {};
    if (path.endsWith("/inboxes")) data = { inboxes: [{ id: "i1", name: "Principal", channel_status: "degraded" }] };
    else if (path.endsWith("/queue")) data = { items: [{ id: "c1", inbox_id: "i1", protocol: "P1", contact_label: "Cliente", contact_id: "Cliente", assignee_id: "Operador", sector_id: "s1", status: head.status, priority: "normal", unread: false, unread_count: 0, last_message_at: "", wait_since: "" }], counts: { mine: 1, unassigned: 0, all: 1 }, page_cursor: "", event_next_cursor: 1 };
    else if (path.endsWith("/annotations")) data = { priority: "normal", version: 1, read_version: 0, watermark: 0, unread: false, unread_count: 0, notes: [], tags: [], events: [] };
    else if (path.endsWith("/annotation-tags")) data = [];
    else if (path.endsWith("/sectors")) data = { sectors: [] };
    else if (path.endsWith("/memberships")) data = { memberships: [] };
    else if (path.endsWith("/history")) data = { head, messages: [], timeline: [], next_cursor: 1 };
    else if (path.endsWith("/conversations")) data = { conversations: [head], messages: [], next_cursor: 1 };
    else if (path.endsWith("/messages")) data = { message_id: "m1", attempt_id: "a1", idempotency_id: "k1", state: "queued", replayed: false };
    else if (path.endsWith("/close")) { head.status = "closed"; data = { conversation: head, closure: {}, farewell: null, farewell_failed: true }; }
    else if (path.endsWith("/reopen")) { head.status = "open"; data = head; }
    return new Response(JSON.stringify(data), { status: 200, headers: { "content-type": "application/json" } });
  }));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const user = userEvent.setup();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MemoryRouter><ConversationsPage /></MemoryRouter></QueryClientProvider>);
  await user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
  await user.click(await screen.findByRole("button", { name: /Protocolo P1/ }));
  await user.type(await screen.findByLabelText("Mensagem"), "Olá");
  await user.click(screen.getByRole("button", { name: "Enviar" }));
  await screen.findByText(/resposta do envio não chegou/i);
  await user.click(screen.getByRole("button", { name: "Enviar" }));
  await waitFor(() => expect(requests.filter((r) => r.path.endsWith("/messages"))).toHaveLength(2));
  expect(requests.find((r) => r.path.endsWith("/messages"))?.body).toMatchObject({ expected_version: 3, body: "Olá", client_request_id: expect.any(String) });
  expect(requests.filter((r) => r.path.endsWith("/messages"))[1]?.body).toEqual(requests.find((r) => r.path.endsWith("/messages"))?.body);
  await user.click(screen.getByRole("button", { name: "Assumir" }));
  await waitFor(() => expect(requests.some((r) => r.path.endsWith("/assume"))).toBe(true));
  await user.click(screen.getByRole("button", { name: "Encerrar com motivo" }));
  await user.type(screen.getByLabelText("Motivo"), "Fim");
  await user.selectOptions(screen.getByLabelText("Resultado"), "resolved");
  await user.type(screen.getByLabelText("Síntese"), "Resolvido");
  await user.type(screen.getByLabelText("Despedida"), "Até logo");
  await user.click(screen.getByRole("button", { name: /^Encerrar$/ }));
  expect(await screen.findByRole("alert")).toHaveTextContent("despedida não foi enfileirada");
  expect(requests.find((r) => r.path.endsWith("/close"))?.body).toMatchObject({ expected_version: 3, farewell_body: "Até logo", farewell_request_id: expect.any(String) });
  await user.click(screen.getByRole("button", { name: "Fechar" }));
  await user.click(await screen.findByRole("button", { name: "Reabrir" }));
  await waitFor(() => expect(requests.some((r) => r.path.endsWith("/reopen"))).toBe(true));
});

it("solicita snapshot após ressincronização e fecha o socket", async () => {
  const sockets: FakeSocket[] = [];
  class FakeSocket {
    onopen: (() => void) | null = null;
    onmessage: ((event: { data: string }) => void) | null = null;
    onclose: ((event: { code: number }) => void) | null = null;
    send = vi.fn(); close = vi.fn();
    constructor() { sockets.push(this); }
  }
  vi.stubGlobal("WebSocket", FakeSocket);
  const queryClient = new QueryClient();
  const invalidate = vi.spyOn(queryClient, "invalidateQueries");
  function Probe() { useAttendanceEvents("i1", ["attendance", "t", "u", "s", "i1"]); return null; }
  const view = render(<QueryClientProvider client={queryClient}><Probe /></QueryClientProvider>);
  act(() => { sockets[0]?.onopen?.(); sockets[0]?.onmessage?.({ data: JSON.stringify({ type: "events", inbox_id: "i1", next_cursor: 42, events: [] }) }); sockets[0]?.onmessage?.({ data: JSON.stringify({ type: "resync", inbox_id: "i1" }) }); });
  expect(sockets[0]?.send).toHaveBeenCalledWith(JSON.stringify({ type: "snapshot", inbox_id: "i1" }));
  expect(invalidate).toHaveBeenCalled();
  view.unmount();
  expect(sockets[0]?.close).toHaveBeenCalled();
});

it("mantém o socket e usa o callback atual após rerender com chave equivalente", () => {
  const sockets: Socket[] = [];
  class Socket { onopen?: () => void; onmessage?: (event: { data: string }) => void; onclose?: (event: { code: number; reason: string }) => void; send = vi.fn(); close = vi.fn(); constructor() { sockets.push(this); } }
  vi.stubGlobal("WebSocket", Socket);
  const first = vi.fn(), second = vi.fn(), qc = new QueryClient();
  function Probe({ callback }: { callback: typeof first }) { useAttendanceEvents("i1", ["attendance", "t", "u", "s", "agent", "m", "i1"], callback); return null; }
  const view = render(<QueryClientProvider client={qc}><Probe callback={first} /></QueryClientProvider>);
  act(() => { sockets[0]?.onopen?.(); sockets[0]?.onmessage?.({ data: JSON.stringify({ type: "snapshot", inbox_id: "i1" }) }); });
  view.rerender(<QueryClientProvider client={qc}><Probe callback={second} /></QueryClientProvider>);
  act(() => sockets[0]?.onmessage?.({ data: JSON.stringify({ type: "events", inbox_id: "i1", events: [{ ID: "e1", Seq: 1, Type: "conversation.opened" }] }) }));
  expect(sockets).toHaveLength(1); expect(sockets[0]?.send).toHaveBeenCalledTimes(1);
  expect(first).not.toHaveBeenCalled(); expect(second).toHaveBeenCalledOnce();
  view.unmount();
});

it("propaga perda de sessão em resposta HTTP 401", async () => {
  const handler = vi.fn(); setSessionLossHandler(handler);
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ detail: "sessão" }), { status: 401, headers: { "content-type": "application/problem+json" } })));
  const { listInboxes } = await import("@/api/attendance");
  await expect(listInboxes()).rejects.toMatchObject({ status: 401 });
  expect(handler).toHaveBeenCalledOnce();
});

it("expõe conflito de versão do servidor", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ detail: "versão obsoleta" }), { status: 409, headers: { "content-type": "application/problem+json" } })));
  const { assume } = await import("@/api/attendance");
  await expect(assume("i1", "c1", { expected_version: 3 })).rejects.toMatchObject({ status: 409, message: "versão obsoleta" });
});

it("preserva versões em respostas perdidas e interpreta replay persistido", async () => {
  vi.stubGlobal("WebSocket", undefined);
  let version = 3, assumeCalls = 0, closeCalls = 0, sendState = "uncertain";
  const requests: { path: string; body: unknown }[] = [];
  const expectedVersion = (body: unknown) =>
    typeof body === "object" && body !== null && "expected_version" in body ? body.expected_version : undefined;
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    const body = request.method === "POST" ? await request.json() : null;
    requests.push({ path, body });
    const head = { id: "c1", protocol: "P1", status: "open", version };
    if (path.endsWith("/assume") && ++assumeCalls === 1) throw new TypeError("rede");
    if (path.endsWith("/assume")) return new Response('{}', { status: 409, headers: { "content-type": "application/json" } });
    if (path.endsWith("/close") && ++closeCalls === 1) return new Response('{}', { status: 409, headers: { "content-type": "application/json" } });
    const data = path.endsWith("/inboxes") ? { inboxes: [] } : path.endsWith("/history") ? { head, messages: [], timeline: [] } : path.endsWith("/conversations") ? { conversations: [head] } : path.endsWith("/messages") ? { state: sendState, replayed: true, message_id: "m", attempt_id: "a", idempotency_id: "k" } : { farewell: null, farewell_failed: false };
    return new Response(JSON.stringify(data), { status: 200, headers: { "content-type": "application/json" } });
  }));
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const principal = { tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false };
  const hook = renderHook(() => useAttendance(principal), { wrapper: ({ children }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider> });
  act(() => { hook.result.current.selectInbox("i1"); hook.result.current.setSelected("c1"); });
  await waitFor(() => expect(hook.result.current.detail.data?.head.version).toBe(3));
  await act(async () => { await hook.result.current.assume("c1"); });
  version = 5;
  await act(async () => { await qc.invalidateQueries({ queryKey: ["attendance"] }); });
  await act(async () => { await hook.result.current.assume("c1"); });
  expect(requests.filter((r) => r.path.endsWith("/assume")).map((r) => expectedVersion(r.body))).toEqual([3, 3]);
  let sendResult: unknown;
  await act(async () => { sendResult = await hook.result.current.send("c1", { intentKey: "send", text: "Olá" }); });
  expect(sendResult).toMatchObject({ outcome: "uncertain" });
  for (const state of ["failed", "cancelled"]) { sendState = state; await act(async () => { sendResult = await hook.result.current.send("c1", { intentKey: "send", text: "Olá" }); }); expect(sendResult).toMatchObject({ outcome: "failed" }); }
  const close = { conversationId: "c1", clientRequestId: "close1", reason: "Fim", result: "resolved" as const, synthesis: "Ok", farewellBody: "Até", farewellRequestId: "bye" };
  await act(async () => { await hook.result.current.close(close); });
  version = 6;
  await act(async () => { await qc.invalidateQueries({ queryKey: ["attendance"] }); });
  await waitFor(() => expect(hook.result.current.detail.data?.head.version).toBe(6));
  let closeResult: unknown;
  await act(async () => { closeResult = await hook.result.current.close({ ...close, clientRequestId: "close2" }); });
  expect(requests.filter((r) => r.path.endsWith("/close")).map((r) => expectedVersion(r.body))).toEqual([5, 6]);
  expect(closeResult).toMatchObject({ outcome: "done", farewellFailed: true });
});

it("limita resync por carga e encerra sessão revogada pelo socket", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ detail: "sessão revogada" }), { status: 401, headers: { "content-type": "application/json" } })));
  const sockets: Socket[] = [];
  class Socket { onopen?: () => void; onmessage?: (event: { data: string }) => void; onclose?: (event: { code: number; reason: string }) => void; send = vi.fn(); close = vi.fn(); constructor() { sockets.push(this); } }
  vi.stubGlobal("WebSocket", Socket);
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const qc = new QueryClient(); qc.setQueryData(["attendance", "t", "u", "s", "i1", "history"], { secret: "texto" });
  function Probe() { return <p>{useAttendanceEvents("i1", ["attendance", "t", "u", "s", "i1"])}</p>; }
  render(<QueryClientProvider client={qc}><Probe /></QueryClientProvider>);
  act(() => { sockets[0]?.onopen?.(); for (let i = 0; i < 3; i++) sockets[0]?.onmessage?.({ data: JSON.stringify({ type: "resync", reason: "load_limit_exceeded", inbox_id: "i1" }) }); });
  expect(sockets[0]?.send).toHaveBeenCalledTimes(1);
  expect(screen.getByText("degraded")).toBeInTheDocument();
  act(() => sockets[0]?.onclose?.({ code: 1008, reason: "authorization_revoked" }));
  await waitFor(() => expect(useSessionStore.getState().principal).toBeNull());
  expect(qc.getQueryData(["attendance", "t", "u", "s", "i1", "history"])).toBeUndefined();
});

it("limpa dados da caixa sem encerrar a sessão ou reconectar quando o acesso à caixa é perdido", async () => {
  const sockets: Socket[] = [];
  class Socket { onopen?: () => void; onmessage?: (event: { data: string }) => void; onclose?: (event: { code: number; reason: string }) => void; send = vi.fn(); close = vi.fn(); constructor() { sockets.push(this); } }
  vi.stubGlobal("WebSocket", Socket);
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const qc = new QueryClient();
  qc.setQueryData(["attendance", "t", "u", "s", "agent", "m", "i1", "history", "c"], { secret: "texto" });
  const directoryKey = ["m3b-directory", "t", "u", "s", "agent", "m", "i1"];
  qc.setQueryData(directoryKey, [{ membership_id: "other", label: "other@example.com" }]);
  function Probe() { return <p>{useAttendanceEvents("i1", ["attendance", "t", "u", "s", "agent", "m", "i1"])}</p>; }
  render(<QueryClientProvider client={qc}><Probe /></QueryClientProvider>);
  act(() => sockets[0]?.onclose?.({ code: 1008, reason: "authorization_lost" }));
  expect(await screen.findByText("revoked")).toBeInTheDocument();
  expect(useSessionStore.getState().principal?.sessionId).toBe("s");
  expect(qc.getQueryData(["attendance", "t", "u", "s", "agent", "m", "i1", "history", "c"])).toBeUndefined();
  expect(qc.getQueryData(directoryKey)).toBeUndefined();
  expect(sockets).toHaveLength(1);
});

it.each(["loaded", "pending"])("revoga diretório %s sem conversa selecionada e descarta resposta tardia", async (state) => {
  const sockets: Socket[] = [];
  class Socket { onopen?: () => void; onmessage?: (event: { data: string }) => void; onclose?: (event: { code: number; reason: string }) => void; send = vi.fn(); close = vi.fn(); constructor() { sockets.push(this); } }
  vi.stubGlobal("WebSocket", Socket);
  let resolveDirectory: ((response: Response) => void) | undefined;
  let directorySignal: AbortSignal | undefined;
  let directoryCalls = 0;
  const entry = { membership_id: "outside-current-page", role: "agent", label: "real.operator@example.com" };
  const response = (data: unknown) => new Response(JSON.stringify(data), { headers: { "content-type": "application/json" } });
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const url = new URL(request.url), path = url.pathname;
    if (path.endsWith("/directory")) {
      directoryCalls++;
      directorySignal = request.signal;
      if (state === "pending") return new Promise<Response>(resolve => { resolveDirectory = resolve; });
      return response({ items: url.searchParams.has("after") ? [entry] : [], next_cursor: url.searchParams.has("after") ? null : "next-member" });
    }
    if (path.endsWith("/inboxes")) return response({ inboxes: [{ id: "i1", name: "Principal", channel_status: "degraded" }] });
    if (path.endsWith("/queue")) return response({ items: [], counts: { mine: 0, all: 0, unassigned: 0 } });
    if (path.endsWith("/conversations")) return response({ conversations: [], messages: [], next_cursor: 0 });
    if (path.endsWith("/sectors")) return response({ sectors: [] });
    if (path.endsWith("/annotation-tags")) return response([]);
    return response({});
  }));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const key = ["m3b-directory", "t", "u", "s", "agent", "m", "i1"];
  const user = userEvent.setup();
  const view = render(<QueryClientProvider client={qc}><MemoryRouter><ConversationsPage /></MemoryRouter></QueryClientProvider>);
  await user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
  await waitFor(() => expect(directoryCalls).toBeGreaterThan(0));
  // A cached authorized label must also disappear while its refresh is pending.
  if (state === "pending") {
    act(() => { qc.setQueryData(key, [entry]); });
    expect(await screen.findByRole("option", { name: entry.label })).toHaveValue(entry.membership_id);
  }
  if (state === "loaded") {
    expect(await screen.findByRole("option", { name: entry.label })).toHaveValue(entry.membership_id);
    expect(directoryCalls).toBe(2);
    await user.selectOptions(screen.getByLabelText("Atendente"), entry.membership_id);
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.objectContaining({ url: expect.stringContaining(`assignee=${entry.membership_id}`) })));
  }
  expect(screen.queryByLabelText("Mensagem")).not.toBeInTheDocument();
  act(() => sockets[0]?.onclose?.({ code: 1008, reason: "authorization_lost" }));
  expect(await screen.findByText("Acesso à caixa encerrado. Selecione outra caixa.")).toBeInTheDocument();
  expect(qc.getQueryData(key)).toBeUndefined();
  if (state === "pending") {
    expect(directorySignal?.aborted).toBe(true);
    await act(async () => { resolveDirectory?.(response({ items: [entry], next_cursor: null })); });
  }
  // Neither a stale socket frame nor a query refresh may revive the revoked directory.
  act(() => sockets[0]?.onmessage?.({ data: JSON.stringify({ type: "snapshot", inbox_id: "i1" }) }));
  const callsAfterRevocation = directoryCalls;
  await act(async () => { await qc.invalidateQueries({ queryKey: key }); });
  expect(directoryCalls).toBe(callsAfterRevocation);
  expect(qc.getQueryData(key)).toBeUndefined();
  expect(screen.queryByRole("option", { name: entry.label })).not.toBeInTheDocument();
  expect(useSessionStore.getState().principal?.sessionId).toBe("s");
  expect(sockets).toHaveLength(1);
  view.unmount(); qc.clear();
});
