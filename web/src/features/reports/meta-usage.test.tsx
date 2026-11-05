import { screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { renderPanel } from "@/test/render-panel";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";

afterEach(() => { resetSessionStore(); vi.unstubAllGlobals(); });

function session(role: string) {
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role, expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
}

it("mostra o uso por categoria sem valores em dinheiro e conta sem preço à parte", async () => {
  const urls: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (req: Request | string) => {
    const url = typeof req === "string" ? req : req.url;
    urls.push(url);
    if (url.includes("/api/panel/meta-usage")) {
      return new Response(JSON.stringify({ from: "2026-10-07", to: "2026-11-05", rows: [{ category: "service", billable: "false", messages: 7 }, { category: "marketing", billable: "true", messages: 2 }], without_pricing: 3, note: "Contagem do que a Meta informou. Não há valor em dinheiro." }), { headers: { "content-type": "application/json" } });
    }
    return new Response(JSON.stringify({}), { status: 404, headers: { "content-type": "application/json" } });
  }));
  session("admin");
  renderPanel("/relatorios");
  const section = await screen.findByRole("region", { name: "Uso reportado pela Meta" });
  await waitFor(() => expect(section).toHaveTextContent("Não cobrável"));
  expect(section).toHaveTextContent("service");
  expect(section).toHaveTextContent("Cobrável");
  expect(section).toHaveTextContent("Sem informação de preço");
  expect(section.textContent).not.toMatch(/R\$|US\$|\$\s?\d/);
  expect(urls.some((u) => /meta-usage\?from=\d{4}-\d{2}-\d{2}&to=\d{4}-\d{2}-\d{2}/.test(u))).toBe(true);
});

it("não pede nem mostra o uso para atendente", async () => {
  const urls: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (req: Request | string) => { urls.push(typeof req === "string" ? req : req.url); return new Response(JSON.stringify({}), { status: 404, headers: { "content-type": "application/json" } }); }));
  session("agent");
  renderPanel("/relatorios");
  await screen.findByRole("heading", { name: "Relatórios", level: 1 });
  expect(screen.queryByRole("region", { name: "Uso reportado pela Meta" })).toBeNull();
  expect(urls.some((u) => u.includes("meta-usage"))).toBe(false);
});
