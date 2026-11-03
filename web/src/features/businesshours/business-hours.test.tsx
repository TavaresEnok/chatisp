import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { HoursSettings } from "@/features/businesshours/hours-settings";
import { hoursKey } from "@/features/businesshours/cache";
import { intervalError } from "@/features/businesshours/schedule";
import { PanelRequestError } from "@/api/problems";
import type { PanelPrincipal } from "@/stores/session-store";

vi.mock("@/api/businesshours", () => ({
  getHours: vi.fn(),
  putHours: vi.fn(),
  deleteHours: vi.fn(),
  evaluateHours: vi.fn(),
  listHoursInboxes: vi.fn(),
  listHoursSectors: vi.fn(),
}));

import { deleteHours, evaluateHours, getHours, listHoursInboxes, listHoursSectors, putHours } from "@/api/businesshours";

const supervisor: PanelPrincipal = {
  tenantId: "tenant-a", userId: "user-a", sessionId: "sess-a", email: "bia@provedor.test",
  membershipId: "mem-a", role: "supervisor", expiresAt: "2099-01-01", mfaEnrollmentRequired: false,
};
const configuration = {
  version: 2,
  configured: true,
  timezone: "America/Sao_Paulo",
  config: { intervals: [{ day: 1, start: 540, end: 720 }], welcome: "", outside_hours: "Fechado", nobody_available: "", on_call_sector_id: "" },
};

describe("horários", () => {
  beforeEach(() => {
    vi.mocked(getHours).mockReset();
    vi.mocked(putHours).mockReset();
    vi.mocked(deleteHours).mockReset();
    vi.mocked(listHoursInboxes).mockReset();
    vi.mocked(listHoursSectors).mockReset();
    vi.mocked(evaluateHours).mockReset();
    vi.mocked(listHoursInboxes).mockResolvedValue({ inboxes: [{ id: "inbox-a", name: "Principal", channel_status: "connected", connector_type: "meta_cloud", enabled: true, presented_number: null }] });
    vi.mocked(listHoursSectors).mockResolvedValue({ sectors: [{ id: "sector-a", name: "Plantão", active: true, legacy_default: false }] });
    vi.mocked(getHours).mockResolvedValue(configuration);
  });

  it("rejeita sobreposição e deixa o supervisor sem gravação", async () => {
    expect(intervalError([{ day: 1, start: 540, end: 720 }, { day: 1, start: 700, end: 800 }])).toMatch(/sobrepostos/);
    expect(intervalError([{ day: 1, start: 540, end: 720 }, { day: 1, start: 720, end: 800 }])).toBeNull();
    const user = userEvent.setup();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><HoursSettings principal={supervisor} /></QueryClientProvider>);
    expect(listHoursInboxes).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Mostrar horário e plantão" }));
    await user.selectOptions(await screen.findByLabelText("Caixa do horário"), "inbox-a");
    expect(await screen.findByText(/America\/Sao_Paulo/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Substituir horário" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Consultar decisão" })).toBeInTheDocument();
    expect(client.getQueryData(hoursKey(supervisor, "inbox-a", "configuration"))).toEqual(configuration);
    vi.mocked(getHours).mockRejectedValue(new PanelRequestError("Acesso negado.", 403));
    await client.invalidateQueries({ queryKey: hoursKey(supervisor, "inbox-a", "configuration") });
    await waitFor(() => expect(client.getQueryData(hoursKey(supervisor, "inbox-a", "configuration"))).toBeUndefined());
  });

  it("mostra a decisão devolvida pelo servidor", async () => {
    const user = userEvent.setup();
    vi.mocked(evaluateHours).mockResolvedValue({
      ...configuration,
      instant: "2026-11-03T15:00:00.000Z",
      open: false,
      next_opening: null,
      default_sector_id: "sector-a",
      on_call_sector_id: "",
      destination_sector_id: "sector-a",
    });
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><HoursSettings principal={{ ...supervisor, role: "admin" }} /></QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: "Mostrar horário e plantão" }));
    await user.selectOptions(await screen.findByLabelText("Caixa do horário"), "inbox-a");
    await screen.findByRole("button", { name: "Substituir horário" });
    await user.type(screen.getByLabelText("Instante avaliado"), "2026-11-03T12:00");
    await user.click(screen.getByRole("button", { name: "Consultar decisão" }));
    expect(await screen.findByText(/Configuração presente/)).toBeInTheDocument();
    expect(screen.getByText(/Fora do expediente\./)).toBeInTheDocument();
    expect(screen.getByText(/2026-11-03T15:00:00.000Z/)).toBeInTheDocument();
    expect(evaluateHours).toHaveBeenCalledWith("inbox-a", expect.stringMatching(/T/));
  });

  it.each([403, 404])("retira a configuração e os controles quando a avaliação responde %s", async (status) => {
    const user = userEvent.setup();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const message = status === 403 ? "Acesso negado." : "Não encontrado.";
    vi.mocked(evaluateHours).mockRejectedValue(new PanelRequestError(message, status));
    render(<QueryClientProvider client={client}><HoursSettings principal={supervisor} /></QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: "Mostrar horário e plantão" }));
    await user.selectOptions(await screen.findByLabelText("Caixa do horário"), "inbox-a");
    expect(await screen.findByText(/America\/Sao_Paulo/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Instante avaliado"), "2026-11-03T12:00");
    await user.click(screen.getByRole("button", { name: "Consultar decisão" }));
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Consultar decisão" })).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Caixa do horário")).not.toBeInTheDocument();
    expect(screen.queryByText(/America\/Sao_Paulo/)).not.toBeInTheDocument();
    expect(client.getQueryData(hoursKey(supervisor, "inbox-a", "configuration"))).toBeUndefined();
  });

  it.each([
    ["Substituir horário", 403, "Acesso negado."],
    ["Retirar configuração", 404, "Não encontrado."],
  ] as const)("retira a configuração e os controles quando %s responde negação", async (button, status, message) => {
    const user = userEvent.setup();
    const admin = { ...supervisor, role: "admin" as const };
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    vi.mocked(putHours).mockRejectedValue(new PanelRequestError(message, status));
    vi.mocked(deleteHours).mockRejectedValue(new PanelRequestError(message, status));
    render(<QueryClientProvider client={client}><HoursSettings principal={admin} /></QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: "Mostrar horário e plantão" }));
    await user.selectOptions(await screen.findByLabelText("Caixa do horário"), "inbox-a");
    await screen.findByRole("button", { name: "Substituir horário" });
    await user.click(screen.getByRole("button", { name: button }));
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Substituir horário" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Retirar configuração" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Consultar decisão" })).not.toBeInTheDocument();
    expect(client.getQueryData(hoursKey(admin, "inbox-a", "configuration"))).toBeUndefined();
  });

  it("não recria a configuração quando o PUT termina depois da revogação", async () => {
    const user = userEvent.setup();
    const admin = { ...supervisor, role: "admin" as const };
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    let resolvePut: (value: typeof configuration) => void = () => undefined;
    vi.mocked(putHours).mockImplementation(() => new Promise((resolve) => {
      resolvePut = resolve;
    }));
    render(<QueryClientProvider client={client}><HoursSettings principal={admin} /></QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: "Mostrar horário e plantão" }));
    await user.selectOptions(await screen.findByLabelText("Caixa do horário"), "inbox-a");
    await screen.findByRole("button", { name: "Substituir horário" });
    await user.click(screen.getByRole("button", { name: "Substituir horário" }));
    await waitFor(() => expect(putHours).toHaveBeenCalledTimes(1));
    vi.mocked(getHours).mockRejectedValue(new PanelRequestError("Acesso negado.", 403));
    await client.invalidateQueries({ queryKey: hoursKey(admin, "inbox-a", "configuration") });
    expect(await screen.findByText("Acesso negado.")).toBeInTheDocument();
    await act(async () => {
      resolvePut({ ...configuration, version: 99 });
      await Promise.resolve();
    });
    expect(client.getQueryData(hoursKey(admin, "inbox-a", "configuration"))).toBeUndefined();
    expect(screen.queryByText("Horário gravado.")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Substituir horário" })).not.toBeInTheDocument();
    expect(screen.queryByText(/Versão 99/)).not.toBeInTheDocument();
  });

  it("não grava o PUT tardio na sessão que já saiu", async () => {
    const user = userEvent.setup();
    const admin = { ...supervisor, role: "admin" as const };
    const next = { ...admin, sessionId: "sess-b", membershipId: "mem-b" };
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    let resolvePut: (value: typeof configuration) => void = () => undefined;
    vi.mocked(putHours).mockImplementation(() => new Promise((resolve) => {
      resolvePut = resolve;
    }));
    const frame = (current: PanelPrincipal) => (
      <QueryClientProvider client={client}><HoursSettings principal={current} /></QueryClientProvider>
    );
    const view = render(frame(admin));
    await user.click(screen.getByRole("button", { name: "Mostrar horário e plantão" }));
    await user.selectOptions(await screen.findByLabelText("Caixa do horário"), "inbox-a");
    await screen.findByRole("button", { name: "Substituir horário" });
    await user.click(screen.getByRole("button", { name: "Substituir horário" }));
    await waitFor(() => expect(putHours).toHaveBeenCalledTimes(1));
    view.rerender(frame(next));
    await act(async () => {
      resolvePut({ ...configuration, version: 99 });
      await Promise.resolve();
    });
    expect(client.getQueryData(hoursKey(admin, "inbox-a", "configuration"))).toBeUndefined();
    expect(client.getQueryData(hoursKey(next, "inbox-a", "configuration"))).toBeUndefined();
    expect(screen.queryByText("Horário gravado.")).not.toBeInTheDocument();
    expect(screen.queryByText(/Versão 99/)).not.toBeInTheDocument();
  });

  it("não grava o PUT tardio depois de desmontar", async () => {
    const user = userEvent.setup();
    const admin = { ...supervisor, role: "admin" as const };
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    let resolvePut: (value: typeof configuration) => void = () => undefined;
    vi.mocked(putHours).mockImplementation(() => new Promise((resolve) => {
      resolvePut = resolve;
    }));
    const view = render(<QueryClientProvider client={client}><HoursSettings principal={admin} /></QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: "Mostrar horário e plantão" }));
    await user.selectOptions(await screen.findByLabelText("Caixa do horário"), "inbox-a");
    await screen.findByRole("button", { name: "Substituir horário" });
    await user.click(screen.getByRole("button", { name: "Substituir horário" }));
    await waitFor(() => expect(putHours).toHaveBeenCalledTimes(1));
    view.unmount();
    expect(client.getQueryData(hoursKey(admin, "inbox-a", "configuration"))).toBeUndefined();
    await act(async () => {
      resolvePut({ ...configuration, version: 99 });
      await Promise.resolve();
    });
    expect(client.getQueryData(hoursKey(admin, "inbox-a", "configuration"))).toBeUndefined();
  });

  it("mantém foco e o valor completo ao substituir o início do intervalo", async () => {
    const user = userEvent.setup();
    const admin = { ...supervisor, role: "admin" as const };
    vi.mocked(putHours).mockResolvedValue({ ...configuration, version: 3 });
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><HoursSettings principal={admin} /></QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: "Mostrar horário e plantão" }));
    await user.selectOptions(await screen.findByLabelText("Caixa do horário"), "inbox-a");
    const start = await screen.findByLabelText("Início");
    expect(start).toHaveValue(540);
    await user.type(start, "600", { initialSelectionStart: 0, initialSelectionEnd: 3 });
    expect(start).toBeInTheDocument();
    expect(start).toHaveFocus();
    expect(start).toHaveValue(600);
    expect(screen.getByLabelText("Início")).toBe(start);
    const end = screen.getByLabelText("Fim");
    await user.type(end, "900", { initialSelectionStart: 0, initialSelectionEnd: 3 });
    expect(end).toHaveFocus();
    expect(end).toHaveValue(900);
    expect(start).toHaveValue(600);
    await user.click(screen.getByRole("button", { name: "Substituir horário" }));
    await waitFor(() => expect(putHours).toHaveBeenCalled());
    const body = vi.mocked(putHours).mock.calls[0]?.[1];
    expect(body?.config.intervals).toEqual([{ day: 1, start: 600, end: 900 }]);
  });
});
