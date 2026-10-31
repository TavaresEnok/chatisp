import { renderHook, waitFor, act } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { useQueue } from "./use-queue";
import { directory } from "@/api/directory";

afterEach(() => vi.unstubAllGlobals());
it("envia filtros e cursor à fila autorizada e reinicia a paginação ao trocar de aba", async () => {
  const urls: URL[] = [];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => { urls.push(new URL(request.url)); return new Response(JSON.stringify({ items: [], counts: { mine: 0, all: 0, unassigned: 0 }, page_cursor: "next", event_next_cursor: 12 }), { headers: { "content-type": "application/json" } }); }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const principal = { tenantId: "t", userId: "u", sessionId: "s", membershipId: "m", role: "agent" as const, email: "a@b", expiresAt: "2099", mfaEnrollmentRequired: false };
  const { result } = renderHook(() => useQueue(principal, "i"), { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> });
  await waitFor(() => expect(result.current.result.isSuccess).toBe(true));
  act(() => result.current.setCursor("next"));
  await waitFor(() => expect(urls.some(url => url.searchParams.get("page_cursor") === "next")).toBe(true));
  act(() => result.current.setTab("all"));
  await waitFor(() => expect(urls.some(url => url.searchParams.get("tab") === "all" && !url.searchParams.get("page_cursor"))).toBe(true));
  expect(urls.every(url => url.searchParams.get("inbox") === "i")).toBe(true);
});

it("percorre mais de 50 páginas do diretório sem depender da página de conversas", async () => {
  const cursors: (string | null)[] = [];
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const url = new URL(request.url);
    expect(url.pathname).toBe("/api/panel/inboxes/i/directory");
    const cursor = url.searchParams.get("after");
    cursors.push(cursor);
    const page = cursor ? Number(cursor) : 0;
    return new Response(JSON.stringify({ items: Array.from({ length: 100 }, (_, index) => ({ membership_id: `${page}-${index}`, role: "agent", label: `${page}-${index}@example.com` })), next_cursor: page < 50 ? String(page + 1) : null }), { headers: { "content-type": "application/json" } });
  }));
  const entries = await directory("i");
  expect(cursors).toHaveLength(51);
  expect(cursors).toEqual([null, ...Array.from({ length: 50 }, (_, i) => String(i + 1))]);
  expect(entries).toHaveLength(5100);
  expect(entries.at(-1)?.label).toBe("50-99@example.com");
});

it("falha explicitamente se o diretório repetir a continuação", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ items: [], next_cursor: "same" }), { headers: { "content-type": "application/json" } })));
  await expect(directory("i")).rejects.toThrow("continuação repetida");
  expect(fetch).toHaveBeenCalledTimes(2);
});
