import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { renderPanel } from "@/test/render-panel";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
afterEach(() => { resetSessionStore(); vi.unstubAllGlobals(); });
it("renomeia, desativa e ativa com PATCH 200 sem corpo e atualiza a lista", async () => {
  let sector = { id: "s1", name: "Suporte", active: true, legacy_default: false }; let patches = 0; let lists = 0;
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => {
    if (req.method === "PATCH") { sector = { ...sector, ...await req.json() }; patches++; return new Response(null, { status: 200 }); }
    if (new URL(req.url).pathname.endsWith("/sectors")) { lists++; return new Response(JSON.stringify({ sectors: [sector] }), { headers: { "content-type": "application/json" } }); }
    return new Response(JSON.stringify({ membership_ids: [], inbox_ids: [], default_inbox_ids: [] }), { headers: { "content-type": "application/json" } });
  }));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "admin", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const user = userEvent.setup(); renderPanel("/equipes"); await user.click(await screen.findByRole("button", { name: "Suporte — Ativo" }));
  const name = screen.getByLabelText("Nome do setor"); await user.clear(name); await user.type(name, "Atendimento"); await user.click(screen.getByRole("button", { name: "Renomear setor" }));
  await screen.findByRole("button", { name: "Atendimento — Ativo" }); await user.click(screen.getByRole("button", { name: "Desativar setor" }));
  await screen.findByRole("button", { name: "Atendimento — Inativo" }); await user.click(screen.getByRole("button", { name: "Ativar setor" }));
  await screen.findByRole("button", { name: "Atendimento — Ativo" }); expect(patches).toBe(3); expect(lists).toBeGreaterThanOrEqual(4); expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});
it("administra vínculos pelo contrato e restringe operações ao papel", async () => {
  const calls: string[] = [];
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => { calls.push(`${req.method} ${new URL(req.url).pathname}`); const path = new URL(req.url).pathname; const data = path.endsWith("/sectors") ? { sectors: [{ id: "s1", name: "Suporte", active: true, legacy_default: false }] } : { membership_ids: ["m1"], inbox_ids: ["i1"], default_inbox_ids: [] }; return new Response(JSON.stringify(data), { headers: { "content-type": "application/json" } }); }));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "admin", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const user = userEvent.setup(); renderPanel("/equipes"); await user.click(await screen.findByRole("button", { name: /Suporte/ })); await user.click(await screen.findByRole("button", { name: "Definir padrão" })); await waitFor(() => expect(calls).toContain("PUT /api/panel/sectors/s1/inboxes/i1/default"));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "v", sessionId: "s2", email: "v@b.test", membershipId: "m2", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf2"); await screen.findByRole("button", { name: /Suporte/ }); expect(screen.queryByRole("button", { name: "Criar setor" })).not.toBeInTheDocument(); expect(screen.queryByText("m1")).not.toBeInTheDocument();
});
it("descarta setores e vínculos ao mudar papel na mesma sessão", async () => {
  const principal = { tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "admin", expiresAt: "2099-01-01", mfaEnrollmentRequired: false };
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => { const admin = useSessionStore.getState().principal?.role === "admin"; const data = new URL(req.url).pathname.endsWith("/sectors") ? { sectors: admin ? [{ id: "secret", name: "Setor privado", active: true }] : [{ id: "own", name: "Meu setor", active: true }] } : { membership_ids: admin ? ["vinculo privado"] : ["m"], inbox_ids: [], default_inbox_ids: [] }; return new Response(JSON.stringify(data), { headers: { "content-type": "application/json" } }); }));
  useSessionStore.getState().applySession(principal, "csrf"); const user = userEvent.setup(); const view = renderPanel("/equipes");
  await user.click(await screen.findByRole("button", { name: /Setor privado/ })); await screen.findByText("vinculo privado");
  act(() => useSessionStore.getState().applySession({ ...principal, role: "agent" }, "csrf"));
  expect(screen.queryByText("vinculo privado")).not.toBeInTheDocument(); expect(screen.queryByText(/Setor privado/)).not.toBeInTheDocument();
  await screen.findByRole("button", { name: /Meu setor/ }); expect(view.queryClient.getQueryCache().findAll({ queryKey: ["m3"] }).some(q => JSON.stringify(q.state.data)?.includes("vinculo privado"))).toBe(false);
});
it("permite repetir leitura de setores e limpa o destino ao trocar de setor", async () => {
  let reads = 0;
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => {
    const path = new URL(req.url).pathname;
    if (path.endsWith("/sectors")) {
      if (++reads === 1) return new Response(null, { status: 503 });
      return new Response(JSON.stringify({ sectors: [{ id: "s1", name: "Suporte", active: true }, { id: "s2", name: "Vendas", active: true }] }), { headers: { "content-type": "application/json" } });
    }
    return new Response(JSON.stringify({ membership_ids: [], inbox_ids: [], default_inbox_ids: [] }), { headers: { "content-type": "application/json" } });
  }));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "admin", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const user = userEvent.setup(); renderPanel("/equipes");
  await user.click(await screen.findByRole("button", { name: "Tentar novamente setores" }));
  await user.click(await screen.findByRole("button", { name: "Suporte — Ativo" }));
  await user.type(screen.getByLabelText("Identificador do membro ou caixa"), "m1");
  await user.click(screen.getByRole("button", { name: "Vendas — Ativo" }));
  expect(screen.getByLabelText("Identificador do membro ou caixa")).toHaveValue("");
  expect(screen.getByRole("button", { name: "Vincular membro" })).toBeDisabled();
});
