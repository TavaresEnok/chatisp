import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { ConversationsPage } from "@/pages/conversations-page";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";

afterEach(() => {
  resetSessionStore();
  vi.unstubAllGlobals();
});

it("opera atendimento simulado pelo contrato do painel", async () => {
  const requests: { path: string; body: unknown }[] = [];
  const head = { id: "c1", protocol: "SIM-001", contact_id: "cliente", assignee_id: "operador", status: "open", version: 3 };
  vi.stubGlobal("WebSocket", undefined);
  vi.stubGlobal("fetch", vi.fn(async (request: Request) => {
    const path = new URL(request.url).pathname;
    const body = request.method === "POST" ? await request.json() : null;
    requests.push({ path, body });
    let data: unknown = {};
    if (path.endsWith("/inboxes")) data = { inboxes: [{ id: "i1", name: "SIMULAÇÃO", channel_status: "connected" }] };
    else if (path.endsWith("/history")) data = { head, messages: [], timeline: [], next_cursor: 1 };
    else if (path.endsWith("/conversations")) data = { conversations: [head], messages: [], next_cursor: 1 };
    else if (path.endsWith("/messages")) data = { message_id: "m1", attempt_id: "a1", idempotency_id: "k1", state: "queued", replayed: false };
    else if (path.endsWith("/close")) { head.status = "closed"; data = { conversation: head, closure: {}, farewell: null, farewell_failed: false }; }
    return new Response(JSON.stringify(data), { status: 200, headers: { "content-type": "application/json" } });
  }));
  useSessionStore.getState().applySession({ tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test", membershipId: "m", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
  const user = userEvent.setup();
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><MemoryRouter><ConversationsPage /></MemoryRouter></QueryClientProvider>);
  await user.selectOptions(await screen.findByLabelText("Caixa"), "i1");
  await user.click(await screen.findByRole("option", { name: /Protocolo SIM-001/ }));
  await user.click(screen.getByRole("button", { name: "Assumir" }));
  await waitFor(() => expect(requests.some((r) => r.path.endsWith("/assume"))).toBe(true));
  await user.type(await screen.findByLabelText("Mensagem"), "Resposta simulada");
  await user.click(screen.getByRole("button", { name: "Enviar" }));
  await waitFor(() => expect(requests.find((r) => r.path.endsWith("/messages"))?.body).toMatchObject({ expected_version: 3, body: "Resposta simulada", client_request_id: expect.any(String) }));
  await user.click(screen.getByRole("button", { name: "Encerrar com motivo" }));
  await user.type(screen.getByLabelText("Motivo"), "Fim");
  await user.selectOptions(screen.getByLabelText("Resultado"), "resolved");
  await user.type(screen.getByLabelText("Síntese"), "Resolvido");
  await user.click(screen.getByRole("button", { name: /^Encerrar$/ }));
  await waitFor(() => expect(requests.find((r) => r.path.endsWith("/close"))?.body).toMatchObject({ expected_version: 3, reason: "Fim", result: "resolved", summary: "Resolvido", client_request_id: expect.any(String) }));
});

it("não consulta caixas quando a sessão precisa concluir a inscrição MFA", async () => {
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  useSessionStore.getState().applySession({
    tenantId: "t", userId: "u", sessionId: "s", email: "a@b.test",
    membershipId: "m", role: "agent", expiresAt: "2099-01-01",
    mfaEnrollmentRequired: true,
  }, "csrf");
  render(
    <QueryClientProvider client={new QueryClient()}>
      <MemoryRouter initialEntries={["/conversas"]}>
        <Routes>
          <Route path="/conversas" element={<ConversationsPage />} />
          <Route path="/entrar" element={<p>Entrar</p>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(await screen.findByText("Entrar")).toBeInTheDocument();
  expect(fetch).not.toHaveBeenCalled();
});
