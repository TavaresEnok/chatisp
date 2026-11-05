import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NumberHealthPanel } from "@/features/channelhealth/number-health";

vi.mock("@/api/channels", async () => {
  const actual = await vi.importActual<typeof import("@/api/channels")>("@/api/channels");
  return { ...actual, getNumberHealth: vi.fn(), readNumberQuality: vi.fn() };
});

import { getNumberHealth, QualityReadError, readNumberQuality, type NumberHealth } from "@/api/channels";

const healthy: NumberHealth = {
  connector_id: "c-1", name: "Suporte", display_number: "+5511977770001", status: "connected", enabled: true,
  limit: { event: "THROUGHPUT_UPGRADE", current_limit: "TIER_2K", old_limit: "TIER_250", max_daily_conversations: 2000, received_at: "2026-11-05T12:00:00Z" },
  quality: { rating: "GREEN", simulation: true, read_at: "2026-11-05T12:30:00Z" },
  window_days: 7, failed_sends: 3, failures_by_code: [{ code: "graph_131026", count: 2 }, { code: "sem_codigo", count: 1 }],
  uncertain_sends: 1, quarantined_items: 4, last_inbound_at: "2026-11-05T11:00:00Z", open_credential_alert: false,
};

function view() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><NumberHealthPanel scope={["t", "u", "s"]} /></QueryClientProvider>);
}

describe("saúde e limites dos números", () => {
  beforeEach(() => {
    vi.mocked(getNumberHealth).mockReset();
    vi.mocked(readNumberQuality).mockReset();
  });
  afterEach(cleanup);

  it("mostra o limite e a nota como a Meta enviou, e os erros essenciais", async () => {
    vi.mocked(getNumberHealth).mockResolvedValue({ dispatch: "simulation", numbers: [healthy] });
    view();
    const card = await screen.findByRole("article", { name: /Suporte/ });
    expect(card).toHaveTextContent("TIER_2K (antes TIER_250), até 2000 conversas por dia");
    expect(card).toHaveTextContent("evento THROUGHPUT_UPGRADE");
    expect(card).toHaveTextContent("GREEN");
    expect(card).toHaveTextContent("servidor simulado, não é a Meta");
    expect(card).toHaveTextContent("graph_131026: 2, sem_codigo: 1");
    expect(within(card).getByText("Itens em quarentena (7 dias)").nextElementSibling).toHaveTextContent("4");
    expect(within(card).queryByRole("alert")).not.toBeInTheDocument();
  });

  it("explica número sem aviso, nota não lida e credencial inválida", async () => {
    vi.mocked(getNumberHealth).mockResolvedValue({ dispatch: "official", numbers: [{ ...healthy, limit: undefined as never, quality: undefined as never, status: "degraded", open_credential_alert: true, failures_by_code: null, failed_sends: 0, last_inbound_at: "" }] });
    view();
    const card = await screen.findByRole("article", { name: /Suporte/ });
    expect(card).toHaveTextContent("nenhum aviso de limite recebido");
    expect(card).toHaveTextContent("não lida");
    expect(within(card).getByRole("alert")).toHaveTextContent("Credencial inválida");
    expect(within(card).getByText("Última mensagem recebida").nextElementSibling).toHaveTextContent("nunca");
  });

  it("lê a nota sob demanda e explica a leitura indisponível", async () => {
    const user = userEvent.setup();
    vi.mocked(getNumberHealth).mockResolvedValue({ dispatch: "simulation", numbers: [healthy] });
    vi.mocked(readNumberQuality).mockRejectedValueOnce(new QualityReadError(503)).mockRejectedValueOnce(new QualityReadError(409));
    view();
    const button = await screen.findByRole("button", { name: "Ler nota de qualidade" });
    await user.click(button);
    expect(await screen.findByText(/integração com a Meta autorizada/)).toBeInTheDocument();
    expect(readNumberQuality).toHaveBeenCalledWith("c-1", expect.anything());
    await user.click(screen.getByRole("button", { name: "Ler nota de qualidade" }));
    expect(await screen.findByText(/recusou a leitura ou o token do canal é inválido/)).toBeInTheDocument();
    expect(getNumberHealth).toHaveBeenCalledTimes(3);
  });

  it("não mostra números quando não há caixa", async () => {
    vi.mocked(getNumberHealth).mockResolvedValue({ dispatch: "official", numbers: [] });
    view();
    expect(await screen.findByText("Nenhum número Meta configurado.")).toBeInTheDocument();
  });

  it("avisa quando o envio pela Meta não está autorizado", async () => {
    vi.mocked(getNumberHealth).mockResolvedValue({ dispatch: "disabled", numbers: [healthy] });
    view();
    expect(await screen.findByText(/Envio pela Meta não autorizado nesta instalação/)).toBeInTheDocument();
    expect(screen.getByText(/driver_indisponivel/)).toBeInTheDocument();
  });
});
