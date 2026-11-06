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

it("exporta o dossiê do titular e só exclui depois da confirmação", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.url;
    if (url.endsWith("/api/panel/retention")) return json(policy);
    if (url.endsWith("/api/panel/plan-limits")) return json(usage);
    if (url.includes("/privacy-export")) {
      return json({
        label: "Exportação dos dados do titular neste atendimento. Não é o histórico completo do provedor nem do ERP. Cópias de backup imutável não entram neste arquivo.",
        contact_id: "c1",
        profile: { name: "Titular Sintético", document_stored: true, document_kind: "cpf" },
        messages: [{ body: "segredo" }],
        demands: [{ protocol: "P-PRIV" }],
      });
    }
    if (url.includes("/privacy-deletion") && init?.method === "POST") {
      return json({
        erased: true,
        already_erased: false,
        protocols: ["P-PRIV"],
        automatic_purge: false,
        note: "O expurgo automático continua desligado. Ciclos, protocolos e motivos permanecem. Backup imutável não é apagado por esta operação.",
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
  await screen.findByDisplayValue("Nexo Fibra");
  const erase = screen.getByRole("button", { name: "Excluir dados do titular" });
  expect(erase).toBeDisabled();
  await user.type(screen.getByLabelText("Contato para exportar"), "c1");
  await user.click(screen.getByRole("button", { name: "Exportar dados do titular" }));
  expect(await screen.findByText(/Titular Sintético/)).toBeInTheDocument();
  expect(screen.getByText(/Cópias de backup imutável não entram neste arquivo/)).toBeInTheDocument();
  expect(erase).toBeDisabled();
  await user.type(screen.getByLabelText("Confirmação da exclusão"), "excluir titular");
  await user.click(erase);
  expect(await screen.findByText(/Protocolos preservados: P-PRIV/)).toBeInTheDocument();
  expect(screen.getByRole("status")).toHaveTextContent("continua desligado");
});

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { "content-type": "application/json" } });
}

it("não permite sobrescrever a política quando a leitura falha", async () => {
  const fetchMock = vi.fn(async (input: RequestInfo) => {
    const url = typeof input === "string" ? input : input.url;
    return url.endsWith("/api/panel/retention") ? json({}, 503) : json(usage);
  });
  vi.stubGlobal("fetch", fetchMock);
  render(<QueryClientProvider client={new QueryClient()}><RetentionPanel /></QueryClientProvider>);
  expect(await screen.findByRole("alert")).toHaveTextContent("Não foi possível ler a política");
  expect(screen.getByRole("button", { name: "Guardar política" })).toBeDisabled();
  expect(screen.getByLabelText("Nome do provedor")).toBeDisabled();
  expect(fetchMock.mock.calls).toHaveLength(2);
});

it("descarta resultado tardio e confirmação ao trocar de contato", async () => {
  let resolveExport!: (response: Response) => void;
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo) => {
    const url = typeof input === "string" ? input : input.url;
    if (url.endsWith("/api/panel/retention")) return Promise.resolve(json(policy));
    if (url.endsWith("/api/panel/plan-limits")) return Promise.resolve(json(usage));
    return new Promise<Response>((resolve) => { resolveExport = resolve; });
  }));
  const user = userEvent.setup();
  render(<QueryClientProvider client={new QueryClient()}><RetentionPanel /></QueryClientProvider>);
  await screen.findByDisplayValue("Nexo Fibra");
  const contact = screen.getByLabelText("Contato para exportar");
  await user.type(contact, "c1");
  await user.type(screen.getByLabelText("Confirmação da exclusão"), "excluir titular");
  await user.click(screen.getByRole("button", { name: "Exportar dados do titular" }));
  await user.clear(contact);
  await user.type(contact, "c2");
  expect(screen.getByRole("button", { name: "Excluir dados do titular" })).toBeDisabled();
  expect(screen.getByLabelText("Confirmação da exclusão")).toHaveValue("");
  resolveExport(json({ label: "Dossiê antigo", profile: { name: "Titular antigo" }, messages: [] }));
  await vi.waitFor(() => expect(screen.queryByText("Dossiê antigo")).not.toBeInTheDocument());
});

it("descarta exportação iniciada antes da exclusão", async () => {
  let resolveExport!: (response: Response) => void;
  let resolveErase!: (response: Response) => void;
  vi.stubGlobal("fetch", vi.fn((input: RequestInfo) => {
    const url = typeof input === "string" ? input : input.url;
    if (url.endsWith("/api/panel/retention")) return Promise.resolve(json(policy));
    if (url.endsWith("/api/panel/plan-limits")) return Promise.resolve(json(usage));
    if (url.includes("privacy-export")) return new Promise<Response>((resolve) => { resolveExport = resolve; });
    return new Promise<Response>((resolve) => { resolveErase = resolve; });
  }));
  const user = userEvent.setup();
  render(<QueryClientProvider client={new QueryClient()}><RetentionPanel /></QueryClientProvider>);
  await screen.findByDisplayValue("Nexo Fibra");
  await user.type(screen.getByLabelText("Contato para exportar"), "c1");
  await user.click(screen.getByRole("button", { name: "Exportar dados do titular" }));
  await user.type(screen.getByLabelText("Confirmação da exclusão"), "excluir titular");
  await user.click(screen.getByRole("button", { name: "Excluir dados do titular" }));
  expect(screen.getByRole("button", { name: "Exportar dados do titular" })).toBeDisabled();
  resolveErase(json({ already_erased: false, protocols: [], note: "Excluído" }));
  expect(await screen.findByText(/Excluído Protocolos preservados/)).toBeInTheDocument();
  resolveExport(json({ label: "Dossiê anterior", profile: { name: "Titular antigo" }, messages: [] }));
  await vi.waitFor(() => expect(screen.queryByText("Dossiê anterior")).not.toBeInTheDocument());
});

it.each([
  { status: 503, result: {}, expected: /A anonimização pode ter sido aplicada; tente novamente/ },
  { status: 200, result: { already_erased: true, objects_removed: 2, protocols: [], note: "Exclusão retomada." }, expected: /remoção dos anexos pendentes foi retomada e concluída/ },
])("informa o estado da exclusão após resposta $status", async ({ status, result, expected }) => {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo) => {
    const url = typeof input === "string" ? input : input.url;
    if (url.endsWith("/api/panel/retention")) return json(policy);
    if (url.endsWith("/api/panel/plan-limits")) return json(usage);
    return json(result, status);
  }));
  const user = userEvent.setup();
  render(<QueryClientProvider client={new QueryClient()}><RetentionPanel /></QueryClientProvider>);
  await screen.findByDisplayValue("Nexo Fibra");
  await user.type(screen.getByLabelText("Contato para exportar"), "c1");
  await user.type(screen.getByLabelText("Confirmação da exclusão"), "excluir titular");
  await user.click(screen.getByRole("button", { name: "Excluir dados do titular" }));
  expect(await screen.findByRole("status")).toHaveTextContent(expected);
});
