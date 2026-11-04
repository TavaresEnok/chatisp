import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { RetentionPanel } from "@/features/retention/retention-panel";

const policy = {
  display_name: "Nexo Fibra",
  content_days: 30,
  media_days: null,
  envelope_days: null,
  log_days: null,
  job_days: null,
  automatic_purge: false,
};

const usage = {
  agents: 2,
  max_agents: 2,
  numbers: 1,
  max_numbers: 1,
  storage_bytes: 100,
  max_storage_bytes: 100,
  note: "O excesso bloqueia cadastro ou upload novo e não apaga o histórico.",
};

afterEach(() => vi.unstubAllGlobals());

it("mostra o nome, o expurgo desligado e a exportação que não é histórico do ERP", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo) => {
    const url = typeof input === "string" ? input : input.url;
    if (url.endsWith("/api/panel/retention")) return json(policy);
    if (url.endsWith("/api/panel/plan-limits")) return json(usage);
    if (url.includes("/demands")) {
      return json({
        label: "Histórico estruturado de demandas do atendimento. Não é o histórico completo do provedor nem do ERP.",
        contact_id: "c1",
        demands: [{ protocol: "P-77", cycle: 1, opened_at: "2026-11-03T11:00:00Z", reason: "lentidão", synthesis: "cliente encaminhado", forwarding: "referência externa; encaminhado. Não é o histórico do ERP.", author: "pessoa:m1" }],
      });
    }
    return json({}, 404);
  }));
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RetentionPanel />
    </QueryClientProvider>,
  );
  expect(await screen.findByDisplayValue("Nexo Fibra")).toBeInTheDocument();
  expect(screen.getByText(/Expurgo automático desligado/)).toBeInTheDocument();
  expect(screen.getByText(/Não é o histórico completo do provedor nem do ERP/)).toBeInTheDocument();
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  await user.type(screen.getByLabelText("Contato para exportar"), "c1");
  await user.click(screen.getByRole("button", { name: "Exportar demandas" }));
  expect(await screen.findByText(/P-77/)).toBeInTheDocument();
  expect(screen.getAllByText(/Não é o histórico completo do provedor nem do ERP/).length).toBeGreaterThan(1);
});

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { "content-type": "application/json" } });
}
