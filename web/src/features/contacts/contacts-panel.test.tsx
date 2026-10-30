import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import { renderPanel } from "@/test/render-panel";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
afterEach(() => { resetSessionStore(); vi.unstubAllGlobals(); });
it("cria, edita e bloqueia contato usando sessão e CSRF", async () => {
  const calls: { method: string; body: unknown; csrf: string | null }[] = [];
  const contact = { id: "c1", name: "Ana", phone: "+5511999999999", email: "", notes: "", blocked: false, block_reason: "", avatar_reference: null, created_at: "", updated_at: "", last_contact: null };
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => { const body = ["POST", "PUT"].includes(req.method) ? await req.json() : null; calls.push({ method: req.method, body, csrf: req.headers.get("X-CSRF-Token") }); if (req.url.endsWith("/block")) Object.assign(contact, { blocked: true }); else if (body) Object.assign(contact, body); return new Response(JSON.stringify(req.method === "GET" ? { items: [contact], next_offset: null } : contact), { status: req.method === "POST" ? 201 : 200, headers: { "content-type": "application/json" } }); }));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const user = userEvent.setup(); renderPanel("/contatos"); await user.type(screen.getByLabelText("Nome"), "Ana"); await user.type(screen.getByLabelText("Telefone internacional"), "+5511999999999"); await user.click(screen.getByRole("button", { name: "Salvar contato" })); await waitFor(() => expect(calls.some(c => c.method === "POST" && c.csrf === "csrf")).toBe(true));
  await user.click(await screen.findByRole("button", { name: "Ana" })); await user.type(screen.getByLabelText("Observações"), "Retorno"); await user.click(screen.getByRole("button", { name: "Salvar contato" })); await waitFor(() => expect(calls.some(c => c.method === "PUT" && (c.body as { notes?: string }).notes === "Retorno")).toBe(true));
  await user.click(await screen.findByRole("button", { name: "Ana" })); await user.type(screen.getByLabelText("Motivo"), "Solicitação"); await user.click(screen.getByRole("button", { name: "Bloquear" })); await waitFor(() => expect(calls.some(c => (c.body as { blocked?: boolean } | null)?.blocked === true)).toBe(true));
});
it("descarta contato editado e observações ao mudar perfil na mesma sessão", async () => {
  const principal = { tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "supervisor", expiresAt: "2099-01-01", mfaEnrollmentRequired: false };
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ items: useSessionStore.getState().principal?.role === "supervisor" ? [{ id: "c", name: "Privado", phone: "+5511", email: "", notes: "Observação privada", blocked: false }] : [], next_offset: null }), { headers: { "content-type": "application/json" } })));
  useSessionStore.getState().applySession(principal, "csrf"); const user = userEvent.setup(); const view = renderPanel("/contatos"); await user.click(await screen.findByRole("button", { name: "Privado" })); expect(screen.getByLabelText("Observações")).toHaveValue("Observação privada");
  act(() => useSessionStore.getState().applySession({ ...principal, role: "agent" }, "csrf"));
  expect(screen.getByLabelText("Observações")).toHaveValue(""); expect(screen.queryByText("Privado")).not.toBeInTheDocument(); await screen.findByText("Nenhum contato encontrado.");
  expect(view.queryClient.getQueryCache().findAll({ queryKey: ["m3"] }).some(q => JSON.stringify(q.state.data)?.includes("Observação privada"))).toBe(false);
});
it("impede trocar contato durante bloqueio atrasado e salva no alvo selecionado", async () => {
  const contacts = [{ id: "a", name: "Ana", phone: "+5511999999991", email: "", notes: "", blocked: false }, { id: "b", name: "Bia", phone: "+5511999999992", email: "", notes: "", blocked: false }];
  const writes: { path: string; body: unknown }[] = []; let release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => {
    const path = new URL(req.url).pathname;
    if (req.method === "GET") return new Response(JSON.stringify({ items: contacts, next_offset: null }), { headers: { "content-type": "application/json" } });
    const body = await req.json(); writes.push({ path, body });
    if (path.endsWith("/block")) { await pending; contacts[0]!.blocked = true; return new Response(JSON.stringify(contacts[0]), { headers: { "content-type": "application/json" } }); }
    return new Response(JSON.stringify(contacts[1]), { headers: { "content-type": "application/json" } });
  }));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const user = userEvent.setup(); renderPanel("/contatos"); await user.click(await screen.findByRole("button", { name: "Ana" })); await user.click(screen.getByRole("button", { name: "Bloquear" }));
  await waitFor(() => expect(writes).toHaveLength(1)); expect(screen.getByRole("button", { name: "Bia" })).toBeDisabled(); await user.click(screen.getByRole("button", { name: "Bia" })); expect(screen.getByLabelText("Nome")).toHaveValue("Ana");
  await act(async () => release()); await waitFor(() => expect(screen.getByRole("button", { name: "Bia" })).toBeEnabled()); await user.click(screen.getByRole("button", { name: "Bia" })); await user.click(screen.getByRole("button", { name: "Salvar contato" }));
  await waitFor(() => expect(writes[1]).toMatchObject({ path: "/api/panel/contacts/b", body: { name: "Bia", phone: "+5511999999992" } }));
});
