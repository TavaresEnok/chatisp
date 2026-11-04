import { screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
import { renderPanel } from "@/test/render-panel";

function isBranding(input: RequestInfo | URL) {
  const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
  return new URL(url, "http://painel.test").pathname === "/api/panel/branding";
}

function brandingResponse() {
  return new Response(JSON.stringify({ display_name: "", fallback_name: "Chat ISP", has_logo: false }), {
    headers: { "content-type": "application/json" },
  });
}

beforeEach(() => useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf"));
afterEach(() => { resetSessionStore(); vi.unstubAllGlobals(); });
it("mostra carregamento na consulta de contatos", async () => {
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo | URL) => isBranding(input) ? Promise.resolve(brandingResponse()) : new Promise(() => {})));
  renderPanel("/contatos");
  expect(await within(screen.getByRole("main")).findByRole("status")).toHaveTextContent("Carregando contatos");
});
it("oculta detalhes técnicos de erros", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    if (isBranding(input)) return brandingResponse();
    return new Response(JSON.stringify({ detail: "backend offline" }), { status: 503, headers: { "content-type": "application/problem+json" } });
  }));
  renderPanel("/contatos");
  expect(await within(screen.getByRole("main")).findByRole("alert")).toHaveTextContent("Não foi possível carregar");
  expect(document.body.textContent).not.toContain("backend offline");
});
it("mostra lista vazia do contrato real", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    if (isBranding(input)) return brandingResponse();
    return new Response(JSON.stringify({ items: [], next_offset: null }), { headers: { "content-type": "application/json" } });
  }));
  renderPanel("/contatos");
  expect(await screen.findByText("Nenhum contato encontrado.")).toBeInTheDocument();
});
