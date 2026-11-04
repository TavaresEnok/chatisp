import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { useState } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import { OpsAlerts } from "@/features/opsalert/ops-alerts";

beforeEach(() => {
  vi.unstubAllGlobals();
});

function Host() {
  const [client] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false } } }));
  return (
    <QueryClientProvider client={client}>
      <OpsAlerts sessionId="sessao" />
    </QueryClientProvider>
  );
}

it("diz que não há alerta e não inventa exemplo", async () => {
  const secret = "Olá, preciso da segunda via do boleto 390.533.447-05";
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
    job_age_seconds: null,
    webhook_age_seconds: null,
    settings_version: 0,
    configured: false,
    alerts: [],
  }), { headers: { "content-type": "application/json" } })));

  render(<Host />);

  expect(await screen.findByText("Não há alerta.")).toBeInTheDocument();
  expect(screen.queryByRole("list", { name: "Alertas abertos" })).not.toBeInTheDocument();
  expect(document.body.textContent ?? "").not.toContain(secret);
  expect(document.body.textContent ?? "").not.toContain("exemplo");
  expect(screen.getByText(/GET \/metrics/)).toBeInTheDocument();
  expect(screen.getByText(/não mostra esses totais/)).toBeInTheDocument();
  expect(document.body.textContent ?? "").not.toContain("aceito_como_capacidade");
  expect(screen.getByLabelText("Idade do job em segundos")).toHaveValue("");
  expect(screen.getByLabelText("Idade do webhook pendente em segundos")).toHaveValue("");
});
