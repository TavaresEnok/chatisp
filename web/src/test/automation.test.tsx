import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AutomationSettings } from "@/features/automation/automation-settings";
import { SnoozePanel } from "@/features/automation/snooze-panel";
import { resetSessionStore, useSessionStore, type PanelPrincipal } from "@/stores/session-store";

const admin: PanelPrincipal = { tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "admin", expiresAt: "2099-01-01", mfaEnrollmentRequired: false };
type Call = { method: string; path: string; body: unknown };
let calls: Call[] = [];
function json(data: unknown, status = 200) { return new Response(JSON.stringify(data), { status, headers: { "content-type": "application/json" } }); }

function stub(routes: (call: Call) => Response | undefined) {
  calls = [];
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => {
    const path = new URL(req.url).pathname;
    const body = ["PUT", "POST"].includes(req.method) ? await req.json() : null;
    const call = { method: req.method, path, body }; calls.push(call);
    const routed = routes(call);
    if (routed) return routed;
    if (path === "/api/panel/inboxes") return json({ inboxes: [{ id: "i1", name: "Comercial", channel_status: "connected" }] });
    if (path === "/api/panel/sectors") return json({ sectors: [{ id: "s1", name: "Financeiro", active: true }] });
    if (path === "/api/panel/lifecycle/inactivity") return json({ configured: false, version: 0, warning_after_seconds: 900, close_after_seconds: 900 });
    if (path === "/api/panel/presence/settings") return json({ configured: false, version: 0, offline_tolerance_seconds: 300 });
    if (path.endsWith("/reopen-window")) return json({ inbox_id: "i1", hours: call.method === "PUT" ? (body as { hours: number }).hours : 0 });
    if (path.endsWith("/triage-menu")) return json({ configured: false, version: 0, max_attempts: 3, options: null });
    return json({}, 404);
  }));
}
function renderWith(node: React.ReactNode) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{node}</QueryClientProvider>);
}
beforeEach(() => useSessionStore.getState().applySession(admin, "csrf"));
afterEach(() => { resetSessionStore(); vi.unstubAllGlobals(); });

it("admin salva a janela de reabertura e o menu com opção de atendente obrigatória", async () => {
  stub(() => undefined);
  const user = userEvent.setup();
  renderWith(<AutomationSettings principal={admin} />);
  await user.click(screen.getByRole("button", { name: "Mostrar automação do atendimento" }));
  await user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
  const reopen = await screen.findByRole("group", { name: "Reabertura automática" });
  expect(await within(reopen).findByText("Reabertura automática desligada.")).toBeInTheDocument();
  const hours = within(reopen).getByLabelText("Janela X (horas)");
  await user.clear(hours); await user.type(hours, "24");
  await user.click(within(reopen).getByRole("button", { name: "Salvar janela" }));
  await waitFor(() => expect(calls.find(c => c.method === "PUT" && c.path === "/api/panel/inboxes/i1/reopen-window")?.body).toEqual({ hours: 24 }));
  expect(await within(reopen).findByText("Reabre até 24 h após o encerramento.")).toBeInTheDocument();

  const menu = screen.getByRole("group", { name: "Menu de triagem" });
  await waitFor(() => expect(within(menu).getByRole("button", { name: "Salvar menu" })).toBeEnabled());
  await user.click(within(menu).getByRole("button", { name: "Adicionar opção" }));
  const second = within(menu).getByRole("listitem", { name: "Opção 2" });
  await user.type(within(second).getByLabelText("Rótulo"), "Segunda via");
  expect(within(menu).getByRole("button", { name: "Salvar menu" })).toBeDisabled();
  await user.selectOptions(within(second).getByLabelText("Setor"), "s1");
  await user.type(within(second).getByLabelText("Motivo"), "financeiro");
  // Turning the only human option into a sector option blocks saving.
  const first = within(menu).getByRole("listitem", { name: "Opção 1" });
  await user.selectOptions(within(first).getByLabelText("Destino"), "sector");
  expect(within(menu).getByRole("alert")).toHaveTextContent("Inclua uma opção para falar com atendente");
  await user.selectOptions(within(first).getByLabelText("Destino"), "human");
  await user.clear(within(first).getByLabelText("Rótulo")); await user.type(within(first).getByLabelText("Rótulo"), "Atendente");
  await user.click(within(menu).getByRole("button", { name: "Salvar menu" }));
  await waitFor(() => expect(calls.find(c => c.method === "PUT" && c.path === "/api/panel/inboxes/i1/triage-menu")?.body).toMatchObject({
    expected_version: 0, max_attempts: 3, request_id: expect.any(String),
    options: [{ key: "1", label: "Atendente", destination: "human", reason: "" }, { key: "2", label: "Segunda via", destination: "sector", sector_id: "s1", reason: "financeiro" }],
  }));
});

it("supervisor consulta sem controles de escrita", async () => {
  const supervisor = { ...admin, role: "supervisor" as const };
  useSessionStore.getState().applySession(supervisor, "csrf");
  stub(() => undefined);
  const user = userEvent.setup();
  renderWith(<AutomationSettings principal={supervisor} />);
  await user.click(screen.getByRole("button", { name: "Mostrar automação do atendimento" }));
  await user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
  await screen.findByRole("group", { name: "Menu de triagem" });
  expect(screen.queryByRole("button", { name: /^Salvar/ })).not.toBeInTheDocument();
  expect(screen.getByLabelText("Aviso após (minutos)")).toBeDisabled();
  expect(calls.every(c => c.method === "GET")).toBe(true);
});

it("adia a conversa com motivo e mostra o retorno previsto", async () => {
  const until = new Date(Date.now() + 3_600_000).toISOString();
  stub(call => {
    if (call.path === "/api/panel/conversations/c1/snooze" && call.method === "GET") return json({}, 404);
    if (call.path === "/api/panel/conversations/c1/snooze" && call.method === "PUT") return json({ id: "z", conversation_id: "c1", state: "active", reason: "aguardar boleto", snoozed_until: until, version: 1 });
    return undefined;
  });
  const onChanged = vi.fn();
  const user = userEvent.setup();
  renderWith(<SnoozePanel principal={admin} inbox="i1" conversation="c1" version={4} status="open" onChanged={onChanged} />);
  await user.click(await screen.findByRole("button", { name: "Adiar" }));
  await user.click(screen.getByRole("button", { name: "4 horas" }));
  expect(screen.getByRole("button", { name: "Confirmar adiamento" })).toBeDisabled();
  await user.type(screen.getByLabelText("Motivo"), "aguardar boleto");
  await user.click(screen.getByRole("button", { name: "Confirmar adiamento" }));
  await waitFor(() => expect(onChanged).toHaveBeenCalled());
  expect(calls.find(c => c.method === "PUT")?.body).toMatchObject({ inbox_id: "i1", expected_version: 4, reason: "aguardar boleto", request_id: expect.any(String), until: expect.any(String) });
  expect(await screen.findByRole("status")).toHaveTextContent("Adiada até");
});

it("não oferece adiamento para conversa encerrada", async () => {
  stub(call => call.path.endsWith("/snooze") ? json({}, 404) : undefined);
  renderWith(<SnoozePanel principal={admin} inbox="i1" conversation="c1" version={4} status="closed" onChanged={() => undefined} />);
  await waitFor(() => expect(calls.some(c => c.path.endsWith("/snooze"))).toBe(true));
  expect(screen.queryByRole("button", { name: "Adiar" })).not.toBeInTheDocument();
});
