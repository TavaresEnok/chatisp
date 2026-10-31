import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { useAttendanceEvents } from "./use-attendance-events";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";

const principal = { tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role: "agent", email: "a@b.test", expiresAt: "2099-01-01", mfaEnrollmentRequired: false };
const key = ["attendance", "t", "u", "s", "agent", "m", "i1"];
const session = { tenant_id: "t", user_id: "u", session_id: "s", membership_id: "m", role: "agent" };
afterEach(() => { resetSessionStore(); localStorage.clear(); vi.unstubAllGlobals(); });

function setup() {
  const sockets: Socket[] = [];
  class Socket {
    onopen?: () => void;
    onmessage?: (event: { data: string }) => void;
    onclose?: (event: { code: number; reason: string }) => void;
    send = vi.fn(); close = vi.fn();
    constructor() { sockets.push(this); }
  }
  vi.stubGlobal("WebSocket", Socket);
  useSessionStore.getState().applySession(principal, "csrf");
  localStorage.setItem("other-draft", "pending-intent");
  const client = new QueryClient();
  client.setQueryData([...key, "history", "lost"], "private");
  client.setQueryData(["other-inbox"], "allowed");
  const hook = renderHook(() => useAttendanceEvents("i1", key), { wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> });
  return { sockets, client, ...hook };
}

it.each([401, 403])("encerra somente após confirmação HTTP %s de revogação", async status => {
  let resolve!: (response: Response) => void;
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(done => { resolve = done; })));
  const h = setup();
  act(() => h.sockets[0]?.onclose?.({ code: 1008, reason: "authorization_revoked" }));
  expect(h.result.current).toBe("checking");
  expect(h.client.getQueryData([...key, "history", "lost"])).toBeUndefined();
  expect(useSessionStore.getState().principal).toEqual(principal);
  expect(localStorage.getItem("other-draft")).toBe("pending-intent");
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
  await act(async () => { resolve(new Response("{}", { status, headers: { "content-type": "application/json" } })); });
  await waitFor(() => expect(useSessionStore.getState().endedReason).toBe("ended"));
  expect(h.client.getQueryData(["other-inbox"])).toBeUndefined();
  expect(localStorage.getItem("other-draft")).toBeNull();
  h.unmount(); h.client.clear();
});

it.each(["network", "server"])("preserva sessão e intenções em falha %s sem reconectar", async failure => {
  vi.stubGlobal("fetch", vi.fn(async () => {
    if (failure === "network") throw new TypeError("offline");
    return new Response("{}", { status: 503, headers: { "content-type": "application/json" } });
  }));
  const h = setup();
  act(() => h.sockets[0]?.onclose?.({ code: 1008, reason: "authorization_revoked" }));
  await waitFor(() => expect(h.result.current).toBe("unavailable"));
  expect(useSessionStore.getState().principal).toEqual(principal);
  expect(localStorage.getItem("other-draft")).toBe("pending-intent");
  expect(h.sockets).toHaveLength(1);
  h.unmount(); h.client.clear();
});

it("reconstrói uma única assinatura autorizada e ignora frames do socket antigo", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify(session), { headers: { "content-type": "application/json" } })));
  const h = setup();
  act(() => h.sockets[0]?.onclose?.({ code: 1008, reason: "authorization_revoked" }));
  await waitFor(() => expect(h.sockets).toHaveLength(2));
  act(() => h.sockets[1]?.onopen?.());
  expect(h.sockets[1]?.send).toHaveBeenCalledWith(JSON.stringify({ type: "subscribe", inbox_id: "i1", with_snapshot: true }));
  act(() => h.sockets[0]?.onmessage?.({ data: JSON.stringify({ type: "snapshot", inbox_id: "i1" }) }));
  act(() => h.sockets[1]?.onclose?.({ code: 1008, reason: "authorization_revoked" }));
  await waitFor(() => expect(h.result.current).toBe("unavailable"));
  expect(h.sockets).toHaveLength(2);
  expect(useSessionStore.getState().principal).toEqual(principal);
  h.unmount(); h.client.clear();
});

it("ignora confirmação antiga depois da troca de caixa", async () => {
  let resolve!: (response: Response) => void;
  vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(done => { resolve = done; })));
  const h = setup();
  act(() => h.sockets[0]?.onclose?.({ code: 1008, reason: "authorization_revoked" }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
  h.unmount();
  await act(async () => { resolve(new Response(JSON.stringify(session), { headers: { "content-type": "application/json" } })); });
  expect(h.sockets).toHaveLength(1);
  expect(useSessionStore.getState().principal).toEqual(principal);
  h.client.clear();
});
