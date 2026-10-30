import { screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
import { renderPanel } from "@/test/render-panel";
beforeEach(() => useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf"));
afterEach(() => { resetSessionStore(); vi.unstubAllGlobals(); });
it("mostra carregamento na consulta de contatos", async () => { vi.stubGlobal("fetch", vi.fn(() => new Promise(() => {}))); renderPanel("/contatos"); expect(await screen.findByRole("status")).toHaveTextContent("Carregando contatos"); });
it("oculta detalhes técnicos de erros", async () => { vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ detail: "backend offline" }), { status: 503, headers: { "content-type": "application/problem+json" } }))); renderPanel("/contatos"); expect(await screen.findByRole("alert")).toHaveTextContent("Não foi possível carregar"); expect(document.body.textContent).not.toContain("backend offline"); });
it("mostra lista vazia do contrato real", async () => { vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ items: [], next_offset: null }), { headers: { "content-type": "application/json" } }))); renderPanel("/contatos"); expect(await screen.findByText("Nenhum contato encontrado.")).toBeInTheDocument(); });
