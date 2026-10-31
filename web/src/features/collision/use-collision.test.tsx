import { renderHook, waitFor } from "@testing-library/react";
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
