import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { setSessionLossHandler } from "@/api/client";
import { DashboardPage } from "@/pages/dashboard-page";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";

vi.mock("@/features/reports/attendance-report", () => ({ AttendanceReportView: () => null }));
vi.mock("@/components/layout/area-page", () => ({ AreaPage: () => null }));
function session(role: string) {
  useSessionStore.getState().applySession({ tenantId: "t", sessionId: "s", userId: "u", membershipId: "m", role, email: "a@example.test", expiresAt: "2099-01-01", mfaEnrollmentRequired: false }, "csrf");
}
function view() {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><DashboardPage /></QueryClientProvider>);
}
function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { "content-type": "application/json" } });
}
beforeEach(() => { resetSessionStore(); vi.restoreAllMocks(); });
it("lista pacotes e links de download para administradores", async () => {
  session("admin");
  const fetcher = vi.fn().mockResolvedValue(json({ packages: [{ id: "docs", title: "Documentação", description: "Guias", filename: "docs.zip", size: 2048, sha256: "abc" }] }));
  vi.stubGlobal("fetch", fetcher);
  view();
  const link = await screen.findByRole("link", { name: "Baixar Documentação" });
  expect(link).toHaveAttribute("href", "/api/panel/packages/docs/download");
  expect(link).toHaveAttribute("download");
  expect(screen.getByText("docs.zip · 2.0 KB")).toBeInTheDocument();
  expect(screen.getByText("abc")).toBeInTheDocument();
  expect(fetcher).toHaveBeenCalledWith(expect.objectContaining({ credentials: "include" }));
});
it("permite tentar novamente e mostra catálogo vazio", async () => {
  session("admin");
  vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(json({}, 503)).mockResolvedValueOnce(json({ packages: [] })));
  view();
  await userEvent.click(await screen.findByRole("button", { name: "Tentar novamente" }));
  expect(await screen.findByText("Nenhum pacote disponível no momento.")).toBeInTheDocument();
});
it("não consulta pacotes para agente ou sessão ausente", async () => {
  const fetcher = vi.fn(); vi.stubGlobal("fetch", fetcher);
  session("agent");
  const rendered = view();
  expect(screen.queryByText("Pacotes para download")).not.toBeInTheDocument();
  resetSessionStore();
  rendered.rerender(<QueryClientProvider client={new QueryClient()}><DashboardPage /></QueryClientProvider>);
  await waitFor(() => expect(fetcher).not.toHaveBeenCalled());
});

it("encaminha perda de sessão ao receber 401", async () => {
  session("admin");
  const onLoss = vi.fn();
  setSessionLossHandler(onLoss);
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(json({}, 401)));
  view();
  await waitFor(() => expect(onLoss).toHaveBeenCalledOnce());
  setSessionLossHandler(null);
});
