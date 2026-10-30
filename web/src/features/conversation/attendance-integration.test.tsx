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
  await user.click(await screen.findByRole("option", { name: /Protocolo P1/ }));
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
  const requests: { path: string; body: any }[] = [];
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
  expect(requests.filter((r) => r.path.endsWith("/assume")).map((r) => r.body.expected_version)).toEqual([3, 3]);
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
  expect(requests.filter((r) => r.path.endsWith("/close")).map((r) => r.body.expected_version)).toEqual([5, 6]);
  expect(closeResult).toMatchObject({ outcome: "done", farewellFailed: true });
});

it("limita resync por carga e encerra sessão revogada pelo socket", async () => {
  const sockets: any[] = [];
  class Socket { onopen?: () => void; onmessage?: (event: { data: string }) => void; onclose?: (event: { code: number; reason: string }) => void; send = vi.fn(); close = vi.fn(); constructor() { sockets.push(this); } }
  vi.stubGlobal("WebSocket", Socket);
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const qc = new QueryClient(); qc.setQueryData(["attendance", "t", "u", "s", "i1", "history"], { secret: "texto" });
  function Probe() { return <p>{useAttendanceEvents("i1", ["attendance", "t", "u", "s", "i1"])}</p>; }
  render(<QueryClientProvider client={qc}><Probe /></QueryClientProvider>);
  act(() => { sockets[0].onopen(); for (let i = 0; i < 3; i++) sockets[0].onmessage({ data: JSON.stringify({ type: "resync", reason: "load_limit_exceeded", inbox_id: "i1" }) }); });
  expect(sockets[0].send).toHaveBeenCalledTimes(1);
  expect(screen.getByText("degraded")).toBeInTheDocument();
  act(() => sockets[0].onclose({ code: 1008, reason: "authorization_revoked" }));
  expect(useSessionStore.getState().principal).toBeNull();
  expect(qc.getQueryData(["attendance", "t", "u", "s", "i1", "history"])).toBeUndefined();
});
