import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useCollision } from "./use-collision";

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });
it("renova apenas a conversa ativa e libera sua aba ao sair", async () => {
  const calls: { method: string; path: string }[] = [];
  vi.spyOn(document, "hasFocus").mockReturnValue(true);
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => { calls.push({ method: request.method, path: new URL(request.url).pathname }); return request.method === "GET" ? new Response(JSON.stringify({ indicators: [], ttl_seconds: 30 }), { headers: { "content-type": "application/json" } }) : new Response(null, { status: 204 }); }));
  const hook = renderHook(({ selected }: { selected: string | null }) => useCollision("i", selected), { initialProps: { selected: "c" as string | null } });
  await waitFor(() => expect(calls.some(call => call.method === "GET" && call.path.endsWith("/collision"))).toBe(true));
  hook.rerender({ selected: null });
  await waitFor(() => expect(calls.some(call => call.method === "DELETE" && call.path.includes("/collision/"))).toBe(true));
});

it("oculta indicadores anteriores ao sair e voltar para a mesma conversa", async () => {
  const pending: Array<(response: Response) => void> = [];
  let gets = 0;
  vi.spyOn(document, "hasFocus").mockReturnValue(true);
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    if (request.method !== "GET") return new Response(null, { status: 204 });
    gets += 1;
    if (gets === 1) return new Response(JSON.stringify({ indicators: [{ membership_id: "old", typing: true, expires_at: "2099-01-01T00:00:00Z" }], ttl_seconds: 30 }), { headers: { "content-type": "application/json" } });
    return new Promise<Response>(resolve => pending.push(resolve));
  }));
  const hook = renderHook(({ selected }: { selected: string | null }) => useCollision("i", selected), { initialProps: { selected: "c" as string | null } });
  await waitFor(() => expect(hook.result.current.people.map(person => person.membership_id)).toEqual(["old"]));
  hook.rerender({ selected: null });
  expect(hook.result.current.people).toEqual([]);
  hook.rerender({ selected: "c" });
  expect(hook.result.current.people).toEqual([]);
  await waitFor(() => expect(gets).toBeGreaterThanOrEqual(2));
  expect(hook.result.current.people).toEqual([]);
  await act(async () => { pending.splice(0).forEach(resolve => resolve(new Response(JSON.stringify({ indicators: [{ membership_id: "new", typing: false, expires_at: "2099-01-01T00:00:00Z" }], ttl_seconds: 30 }), { headers: { "content-type": "application/json" } }))); });
  await waitFor(() => expect(hook.result.current.people.map(person => person.membership_id)).toEqual(["new"]));
});
