import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router";
import { AppProviders } from "@/providers/app-providers";
import { AppRoutes } from "@/routes";
import { resetSessionStore } from "@/stores/session-store";
import { setCsrfToken } from "@/api/client";

afterEach(() => { cleanup(); resetSessionStore(); vi.unstubAllGlobals(); });

it("restaura sessão antes de avaliar o redirecionamento de uma URL protegida", async () => {
  resetSessionStore();
  setCsrfToken("csrf-restored");
  let finish!: (response: Response) => void;
  const pending = new Promise<Response>((resolve) => { finish = resolve; });
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    if (request.url.endsWith("/api/panel/session")) return pending;
    return new Response(JSON.stringify({ inboxes: [], sectors: [], tags: [] }), { headers: { "content-type": "application/json" } });
  }));
  render(<AppProviders><MemoryRouter initialEntries={["/conversas"]}><AppRoutes /></MemoryRouter></AppProviders>);
  expect(screen.getByRole("status")).toHaveTextContent("Verificando sessão");
  expect(screen.queryByLabelText("E-mail")).not.toBeInTheDocument();
  await act(async () => finish(new Response(JSON.stringify({
    user_id: "u1", email: "admin@example.test", tenant_id: "t1", membership_id: "m1", role: "admin",
    session_id: "s1", expires_at: "2099-01-01T00:00:00Z", mfa_enrollment_required: false,
  }), { headers: { "content-type": "application/json" } })));
  expect(await screen.findByRole("heading", { level: 1, name: "Conversas" })).toBeInTheDocument();
  expect(await screen.findByRole("combobox", { name: /Caixa/ })).toBeInTheDocument();
  expect(screen.queryByLabelText("E-mail")).not.toBeInTheDocument();
});

it.each([401, 403, 503])("não monta o painel enquanto verifica uma sessão recusada (%s)", async (status) => {
  setCsrfToken("csrf-expired");
  let finish!: (response: Response) => void;
  const pending = new Promise<Response>((resolve) => { finish = resolve; });
  const fetch = vi.fn(async (request: Request) => {
    expect(new URL(request.url).pathname).toBe("/api/panel/session");
    return pending;
  });
  vi.stubGlobal("fetch", fetch);
  render(<AppProviders><MemoryRouter initialEntries={["/painel"]}><AppRoutes /></MemoryRouter></AppProviders>);
  expect(screen.getByRole("status")).toHaveTextContent("Verificando sessão");
  expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
  expect(screen.queryByRole("heading", { name: "Painel" })).not.toBeInTheDocument();
  await act(async () => finish(new Response(null, { status })));
  expect(await screen.findByRole("heading", { name: "Entrar" })).toBeInTheDocument();
  expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
  expect(fetch).toHaveBeenCalledTimes(1);
});

it("impede entrada anônima sem cookie CSRF durante e após a verificação", async () => {
  let finish!: (response: Response) => void;
  const pending = new Promise<Response>((resolve) => { finish = resolve; });
  vi.stubGlobal("fetch", vi.fn(() => pending));
  render(<AppProviders><MemoryRouter initialEntries={["/painel"]}><AppRoutes /></MemoryRouter></AppProviders>);
  expect(screen.getByRole("heading", { name: "Entrar" })).toBeInTheDocument();
  expect(screen.queryByRole("navigation")).not.toBeInTheDocument();
  await act(async () => finish(new Response(null, { status: 401 })));
  expect(await screen.findByRole("heading", { name: "Entrar" })).toBeInTheDocument();
});
