import type { ReactNode } from "react";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConsentForm, TemplateCatalog, WindowIndicator } from "@/features/channelpolicy/channel-panels";
import { policyKey } from "@/features/channelpolicy/cache";
import { clearTemplateIntents, readTemplateIntent, templateIntentKey, writeTemplateIntent } from "@/features/channelpolicy/template-intent";
import { PanelRequestError } from "@/api/problems";
import type { PanelPrincipal } from "@/stores/session-store";

vi.mock("@/api/channelpolicy", () => ({
  listTemplates: vi.fn(),
  syncTemplates: vi.fn(),
  recordConsent: vi.fn(),
  queueTemplate: vi.fn(),
}));

import { listTemplates, queueTemplate, recordConsent, syncTemplates } from "@/api/channelpolicy";

const principal: PanelPrincipal = {
  tenantId: "tenant-a", userId: "user-a", sessionId: "sess-a", email: "ana@provedor.test",
  membershipId: "mem-a", role: "supervisor", expiresAt: "2099-01-01", mfaEnrollmentRequired: false,
};

const templates = [
  { external_id: "tpl-ok", name: "Boas-vindas", language: "pt_BR", category: "UTILITY", state: "APPROVED", source: "driver", components: [] },
  { external_id: "tpl-fake", name: "Simulado", language: "pt_BR", category: "UTILITY", state: "APPROVED", source: "fake", components: [] },
];

function view(node: ReactNode, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
  return client;
}

describe("política de canal", () => {
  beforeEach(() => {
    clearTemplateIntents();
    vi.mocked(listTemplates).mockReset();
    vi.mocked(queueTemplate).mockReset();
    vi.mocked(recordConsent).mockReset();
    vi.mocked(syncTemplates).mockReset();
    vi.mocked(listTemplates).mockResolvedValue(templates);
  });

  it("mostra a janela oficial só na Cloud e como indisponível", () => {
    const { rerender } = render(<WindowIndicator connectorType="evolution" />);
    expect(screen.queryByText(/Janela oficial/)).not.toBeInTheDocument();
    rerender(<WindowIndicator connectorType="meta_cloud" />);
    expect(screen.getByRole("status")).toHaveTextContent("indisponível neste painel");
    expect(screen.getByRole("status")).toHaveTextContent("não calcula o prazo");
  });

  it("não oferece sincronização ao supervisor e só enfileira template aprovado pelo driver", async () => {
    const user = userEvent.setup();
    vi.mocked(queueTemplate).mockRejectedValueOnce(new Error("rede")).mockResolvedValueOnce({ message_id: "m", attempt_id: "a", idempotency_id: "i", state: "queued", replayed: false });
    view(<TemplateCatalog principal={principal} inboxId="inbox-a" conversationId="conv-a" connectorType="meta_cloud" version={4} />);
    expect(await screen.findByText(/Simulado/)).toBeInTheDocument();
    expect(screen.getByText(/não está aprovado por driver/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Sincronizar catálogo" })).not.toBeInTheDocument();
    await user.type(screen.getByLabelText("Finalidade do template"), "atendimento");
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    expect(queueTemplate).toHaveBeenCalledTimes(2);
    const first = vi.mocked(queueTemplate).mock.calls[0]?.[2];
    const second = vi.mocked(queueTemplate).mock.calls[1]?.[2];
    expect(first).toMatchObject({ template_id: "tpl-ok", language: "pt_BR", purpose: "atendimento", expected_version: 4 });
    expect(second?.client_request_id).toBe(first?.client_request_id);
  });

  it("reutiliza o payload original após a versão mudar e após 502", async () => {
    const user = userEvent.setup();
    vi.mocked(queueTemplate)
      .mockRejectedValueOnce(new Error("rede"))
      .mockRejectedValueOnce(new PanelRequestError("indisponível", 502))
      .mockResolvedValueOnce({ message_id: "m", attempt_id: "a", idempotency_id: "i", state: "queued", replayed: true });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const catalog = (version: number) => (
      <QueryClientProvider client={client}>
        <TemplateCatalog principal={principal} inboxId="inbox-a" conversationId="conv-a" connectorType="meta_cloud" version={version} />
      </QueryClientProvider>
    );
    const { rerender } = render(catalog(4));
    await user.type(await screen.findByLabelText("Finalidade do template"), "atendimento");
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    expect(await screen.findByText(/reutilizar a chave/)).toBeInTheDocument();
    rerender(catalog(11));
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    expect(await screen.findByText(/payload originais continuam válidos/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    expect(queueTemplate).toHaveBeenCalledTimes(3);
    const bodies = vi.mocked(queueTemplate).mock.calls.map((call) => call[2]);
    expect(bodies[0]).toMatchObject({ expected_version: 4, purpose: "atendimento", template_id: "tpl-ok", language: "pt_BR" });
    expect(bodies[1]).toEqual(bodies[0]);
    expect(bodies[2]).toEqual(bodies[0]);
  });

  it("só cria outra chave no novo envio explícito", async () => {
    const user = userEvent.setup();
    vi.mocked(queueTemplate).mockRejectedValueOnce(new Error("rede")).mockResolvedValueOnce({ message_id: "m", attempt_id: "a", idempotency_id: "i", state: "queued", replayed: false });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const catalog = (version: number) => (
      <QueryClientProvider client={client}>
        <TemplateCatalog principal={principal} inboxId="inbox-a" conversationId="conv-a" connectorType="meta_cloud" version={version} />
      </QueryClientProvider>
    );
    const { rerender } = render(catalog(4));
    await user.type(await screen.findByLabelText("Finalidade do template"), "atendimento");
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    await screen.findByText(/reutilizar a chave/);
    rerender(catalog(8));
    await user.click(screen.getByRole("button", { name: "Novo envio do template" }));
    expect(queueTemplate).toHaveBeenCalledTimes(2);
    const [first, second] = vi.mocked(queueTemplate).mock.calls.map((call) => call[2]);
    expect(first?.expected_version).toBe(4);
    expect(second?.expected_version).toBe(8);
    expect(second?.client_request_id).not.toBe(first?.client_request_id);
    expect(second?.purpose).toBe("atendimento");
  });

  it("recupera a mesma chave e o mesmo payload depois de trocar de conversa", async () => {
    const user = userEvent.setup();
    vi.mocked(queueTemplate)
      .mockRejectedValueOnce(new Error("rede"))
      .mockResolvedValueOnce({ message_id: "m", attempt_id: "a", idempotency_id: "i", state: "queued", replayed: true });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const frame = (conversationId: string, version: number) => (
      <QueryClientProvider client={client}>
        <TemplateCatalog key={`templates:${conversationId}`} principal={principal} inboxId="inbox-a" conversationId={conversationId} connectorType="meta_cloud" version={version} />
      </QueryClientProvider>
    );
    const view = render(frame("conv-a", 4));
    await user.type(await screen.findByLabelText("Finalidade do template"), "atendimento");
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    expect(await screen.findByText(/reutilizar a chave/)).toBeInTheDocument();
    view.rerender(frame("conv-b", 9));
    expect(await screen.findByLabelText("Finalidade do template")).toHaveValue("");
    view.rerender(frame("conv-a", 11));
    expect(await screen.findByLabelText("Finalidade do template")).toHaveValue("atendimento");
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    expect(await screen.findByText("Template colocado na fila existente.")).toBeInTheDocument();
    expect(queueTemplate).toHaveBeenCalledTimes(2);
    const [first, second] = vi.mocked(queueTemplate).mock.calls.map((call) => call[2]);
    expect(first).toMatchObject({ template_id: "tpl-ok", language: "pt_BR", purpose: "atendimento", expected_version: 4 });
    expect(second).toEqual(first);
    expect(vi.mocked(queueTemplate).mock.calls[1]?.[1]).toBe("conv-a");
  });

  it("não restaura intenção revogada ao voltar para a conversa", async () => {
    const user = userEvent.setup();
    vi.mocked(queueTemplate)
      .mockRejectedValueOnce(new Error("rede"))
      .mockRejectedValueOnce(new PanelRequestError("Acesso negado.", 403))
      .mockResolvedValueOnce({ message_id: "m", attempt_id: "a", idempotency_id: "i", state: "queued", replayed: false });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const frame = (conversationId: string, version: number) => (
      <QueryClientProvider client={client}>
        <TemplateCatalog key={`templates:${conversationId}`} principal={principal} inboxId="inbox-a" conversationId={conversationId} connectorType="meta_cloud" version={version} />
      </QueryClientProvider>
    );
    const view = render(frame("conv-a", 4));
    await user.type(await screen.findByLabelText("Finalidade do template"), "atendimento");
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    await screen.findByText(/reutilizar a chave/);
    view.rerender(frame("conv-b", 9));
    view.rerender(frame("conv-a", 11));
    await user.click(await screen.findByRole("button", { name: "Colocar template na fila" }));
    expect(await screen.findByText("Acesso negado.")).toBeInTheDocument();
    view.rerender(frame("conv-b", 9));
    view.rerender(frame("conv-a", 12));
    const field = await screen.findByLabelText("Finalidade do template");
    expect(field).toHaveValue("");
    await user.type(field, "atendimento");
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    expect(queueTemplate).toHaveBeenCalledTimes(3);
    const bodies = vi.mocked(queueTemplate).mock.calls.map((call) => call[2]);
    expect(bodies[1]).toEqual(bodies[0]);
    expect(bodies[2]?.client_request_id).not.toBe(bodies[0]?.client_request_id);
    expect(bodies[2]).toMatchObject({ expected_version: 12, purpose: "atendimento", template_id: "tpl-ok" });
  });

  it.each([
    ["sucesso", "resolve"],
    ["erro definitivo", "reject"],
  ] as const)("preserva o envio novo quando o anterior termina por %s depois da troca de conversa", async (_label, settle) => {
    const user = userEvent.setup();
    const queued = { message_id: "m", attempt_id: "a", idempotency_id: "i", state: "queued", replayed: false };
    let resolveA: (value: typeof queued) => void = () => undefined;
    let rejectA: (error: unknown) => void = () => undefined;
    let rejectB: (error: unknown) => void = () => undefined;
    let calls = 0;
    vi.mocked(queueTemplate).mockImplementation(() => {
      calls += 1;
      if (calls === 1) {
        return new Promise((resolve, reject) => {
          resolveA = resolve;
          rejectA = reject;
        });
      }
      if (calls === 2) return new Promise((_resolve, reject) => { rejectB = reject; });
      return Promise.resolve({ ...queued, replayed: true });
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const frame = (conversationId: string, version: number) => (
      <QueryClientProvider client={client}>
        <TemplateCatalog key={`templates:${conversationId}`} principal={principal} inboxId="inbox-a" conversationId={conversationId} connectorType="meta_cloud" version={version} />
      </QueryClientProvider>
    );
    const view = render(frame("conv-a", 4));
    await user.type(await screen.findByLabelText("Finalidade do template"), "atendimento");
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    await waitFor(() => expect(queueTemplate).toHaveBeenCalledTimes(1));
    view.rerender(frame("conv-b", 9));
    view.rerender(frame("conv-a", 11));
    expect(await screen.findByLabelText("Finalidade do template")).toHaveValue("atendimento");
    await user.click(screen.getByRole("button", { name: "Novo envio do template" }));
    await waitFor(() => expect(queueTemplate).toHaveBeenCalledTimes(2));
    const [first, second] = vi.mocked(queueTemplate).mock.calls.map((call) => call[2]);
    expect(second?.client_request_id).not.toBe(first?.client_request_id);
    expect(second).toMatchObject({ expected_version: 11, purpose: "atendimento", template_id: "tpl-ok", language: "pt_BR" });
    const intentKey = templateIntentKey({ ...principal, inboxId: "inbox-a", conversationId: "conv-a", templateId: "tpl-ok", language: "pt_BR" });
    await act(async () => {
      if (settle === "resolve") resolveA(queued);
      else rejectA(new PanelRequestError("Recusado.", 400));
      await Promise.resolve();
    });
    expect(readTemplateIntent(intentKey)?.client_request_id).toBe(second?.client_request_id);
    expect(screen.getByRole("button", { name: "Novo envio do template" })).toBeDisabled();
    expect(screen.queryByText("Template colocado na fila existente.")).not.toBeInTheDocument();
    expect(screen.queryByText("Recusado.")).not.toBeInTheDocument();
    await act(async () => {
      rejectB(new Error("rede"));
      await Promise.resolve();
    });
    expect(await screen.findByText(/A resposta não chegou/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    await waitFor(() => expect(queueTemplate).toHaveBeenCalledTimes(3));
    expect(vi.mocked(queueTemplate).mock.calls[2]?.[2]).toEqual(second);
    expect(vi.mocked(queueTemplate).mock.calls[2]?.[1]).toBe("conv-a");
  });

  it("registra revogação de consentimento sem guardar estado local", async () => {
    const user = userEvent.setup();
    vi.mocked(recordConsent).mockResolvedValue({ invalidated_ids: ["pendencia-1"] });
    view(<><TemplateCatalog principal={principal} inboxId="inbox-a" conversationId="conv-a" connectorType="meta_cloud" version={1} /><ConsentForm principal={principal} inboxId="inbox-a" contactId="contact-a" /></>);
    expect(await screen.findByText(/Boas-vindas/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Finalidade do consentimento"), "atendimento");
    await user.type(screen.getByLabelText("Origem"), "painel");
    await user.type(screen.getByLabelText("Evidência"), "evento-1");
    await user.click(screen.getByRole("checkbox", { name: "Revogar" }));
    await user.click(screen.getByRole("button", { name: "Registrar consentimento" }));
    expect(recordConsent).toHaveBeenCalledWith("inbox-a", "contact-a", { purpose: "atendimento", source: "painel", evidence: "evento-1", revoke: true });
    expect(await screen.findByText(/Pendências invalidadas: pendencia-1/)).toBeInTheDocument();
    expect(screen.getByText(/Não há consulta do estado atual/)).toBeInTheDocument();
  });

  it.each([403, 404])("revoga catálogo e consentimento quando a releitura responde %s e ignora a lista tardia", async (status) => {
    const message = status === 403 ? "Acesso negado." : "Não encontrado.";
    const late: Array<(value: typeof templates) => void> = [];
    let calls = 0;
    vi.mocked(listTemplates).mockImplementation((_inbox, signal) => {
      calls += 1;
      if (calls === 1) return Promise.resolve(templates);
      if (calls === 2) return Promise.reject(new PanelRequestError(message, status));
      return new Promise((resolve, reject) => {
        const abort = () => reject(new DOMException("A consulta foi invalidada.", "AbortError"));
        if (signal?.aborted) {
          abort();
          return;
        }
        signal?.addEventListener("abort", abort, { once: true });
        late.push(resolve);
      });
    });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const frame = (
      <QueryClientProvider client={client}>
        <TemplateCatalog principal={principal} inboxId="inbox-a" conversationId="conv-a" connectorType="meta_cloud" version={4} />
        <ConsentForm principal={principal} inboxId="inbox-a" contactId="contact-a" />
      </QueryClientProvider>
    );
    const rendered = render(frame);
    expect(await screen.findByText(/Boas-vindas/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Colocar template na fila" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Registrar consentimento" })).toBeInTheDocument();
    const intentKey = templateIntentKey({ ...principal, inboxId: "inbox-a", conversationId: "conv-a", templateId: "tpl-ok", language: "pt_BR" });
    writeTemplateIntent(intentKey, {
      client_request_id: "pedido-antigo",
      expected_version: 4,
      language: "pt_BR",
      purpose: "atendimento",
      template_id: "tpl-ok",
    });
    await act(async () => {
      await client.invalidateQueries({ queryKey: policyKey(principal, "inbox-a", "templates") }).catch(() => undefined);
    });
    await waitFor(() => expect(screen.queryByText(/Boas-vindas/)).not.toBeInTheDocument());
    expect(screen.queryByRole("button", { name: "Colocar template na fila" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Registrar consentimento" })).not.toBeInTheDocument();
    expect(screen.getAllByText(message)).toHaveLength(2);
    expect(client.getQueryData(policyKey(principal, "inbox-a", "templates"))).toBeUndefined();
    expect(readTemplateIntent(intentKey)).toBeNull();
    rendered.rerender(frame);
    await act(async () => {
      void client.invalidateQueries({ queryKey: policyKey(principal, "inbox-a", "templates") }).catch(() => undefined);
      await Promise.resolve();
      for (const resolve of late) resolve(templates);
      await Promise.resolve();
    });
    expect(calls).toBe(2);
    expect(screen.queryByText(/Boas-vindas/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Colocar template na fila" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Registrar consentimento" })).not.toBeInTheDocument();
    expect(client.getQueryData(policyKey(principal, "inbox-a", "templates"))).toBeUndefined();
    expect(client.getQueryCache().findAll({ queryKey: policyKey(principal) }).every((query) => query.state.data === undefined)).toBe(true);
  });

  it.each([
    ["uncertain", /resultado do template é incerto/, true],
    ["observed", /estado do template é desconhecido/, true],
    ["failed", /envio do template falhou/, false],
    ["cancelled", /envio do template foi cancelado/, false],
  ] as const)("replay HTTP com estado %s preserva a chave só quando o resultado continua aberto", async (state, message, keep) => {
    const user = userEvent.setup();
    vi.mocked(queueTemplate)
      .mockRejectedValueOnce(new Error("rede"))
      .mockResolvedValueOnce({ message_id: "m", attempt_id: "a", idempotency_id: "i", state, replayed: true })
      .mockResolvedValue({ message_id: "m2", attempt_id: "a2", idempotency_id: "i2", state: "queued", replayed: false });
    view(<TemplateCatalog principal={principal} inboxId="inbox-a" conversationId="conv-a" connectorType="meta_cloud" version={4} />);
    await user.type(await screen.findByLabelText("Finalidade do template"), "atendimento");
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    expect(await screen.findByText(/reutilizar a chave/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.queryByText("Template colocado na fila existente.")).not.toBeInTheDocument();
    const intentKey = templateIntentKey({ ...principal, inboxId: "inbox-a", conversationId: "conv-a", templateId: "tpl-ok", language: "pt_BR" });
    const first = vi.mocked(queueTemplate).mock.calls[0]?.[2];
    if (keep) {
      expect(readTemplateIntent(intentKey)?.client_request_id).toBe(first?.client_request_id);
      expect(screen.getByRole("button", { name: "Novo envio do template" })).toBeEnabled();
    } else {
      expect(readTemplateIntent(intentKey)).toBeNull();
      expect(screen.queryByRole("button", { name: "Novo envio do template" })).not.toBeInTheDocument();
    }
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    await waitFor(() => expect(queueTemplate).toHaveBeenCalledTimes(3));
    const third = vi.mocked(queueTemplate).mock.calls[2]?.[2];
    if (keep) expect(third).toEqual(first);
    else {
      expect(third?.client_request_id).not.toBe(first?.client_request_id);
      expect(third).toMatchObject({ template_id: "tpl-ok", language: "pt_BR", purpose: "atendimento", expected_version: 4 });
    }
  });

  it.each([403, 404])("revoga o catálogo na hora quando o enfileiramento responde %s", async (status) => {
    const user = userEvent.setup();
    const late: Array<(value: typeof templates) => void> = [];
    let calls = 0;
    vi.mocked(listTemplates).mockImplementation(() => {
      calls += 1;
      if (calls === 1) return Promise.resolve(templates);
      return new Promise((resolve) => {
        late.push(resolve);
      });
    });
    vi.mocked(queueTemplate).mockRejectedValue(new PanelRequestError(status === 403 ? "Acesso negado." : "Não encontrado.", status));
    const client = view(<TemplateCatalog principal={principal} inboxId="inbox-a" conversationId="conv-a" connectorType="meta_cloud" version={4} />);
    expect(await screen.findByText(/Boas-vindas/)).toBeInTheDocument();
    expect(client.getQueryData(policyKey(principal, "inbox-a", "templates"))).toEqual(templates);
    void client.invalidateQueries({ queryKey: policyKey(principal, "inbox-a", "templates") }).catch(() => undefined);
    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
    await user.type(screen.getByLabelText("Finalidade do template"), "atendimento");
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    expect(await screen.findByText(status === 403 ? "Acesso negado." : "Não encontrado.")).toBeInTheDocument();
    expect(client.getQueryData(policyKey(principal, "inbox-a", "templates"))).toBeUndefined();
    expect(screen.queryByText(/Boas-vindas/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Colocar template na fila" })).not.toBeInTheDocument();
    await act(async () => {
      for (const resolve of late) resolve(templates);
      await Promise.resolve();
    });
    expect(client.getQueryData(policyKey(principal, "inbox-a", "templates"))).toBeUndefined();
    expect(screen.queryByText(/Boas-vindas/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Colocar template na fila" })).not.toBeInTheDocument();
  });

  it("revoga o catálogo quando sincronizar ou registrar consentimento confirma a perda de acesso", async () => {
    const user = userEvent.setup();
    const admin = { ...principal, role: "admin" as const };
    let releaseSync: () => void = () => undefined;
    vi.mocked(syncTemplates).mockImplementation(() => new Promise((resolve) => {
      releaseSync = () => resolve();
    }));
    vi.mocked(queueTemplate).mockRejectedValueOnce(new PanelRequestError("Acesso negado.", 403));
    const synced = view(<TemplateCatalog principal={admin} inboxId="inbox-a" conversationId="conv-a" connectorType="meta_cloud" version={4} />);
    expect(await screen.findByText(/Boas-vindas/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Sincronizar catálogo" }));
    await user.type(screen.getByLabelText("Finalidade do template"), "atendimento");
    await user.click(screen.getByRole("button", { name: "Colocar template na fila" }));
    expect(await screen.findByText("Acesso negado.")).toBeInTheDocument();
    await act(async () => {
      releaseSync();
      await Promise.resolve();
    });
    expect(synced.getQueryData(policyKey(admin, "inbox-a", "templates"))).toBeUndefined();
    expect(screen.queryByRole("button", { name: "Sincronizar catálogo" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Colocar template na fila" })).not.toBeInTheDocument();
    cleanup();

    vi.mocked(listTemplates).mockResolvedValue(templates);
    vi.mocked(recordConsent).mockRejectedValue(new PanelRequestError("Não encontrado.", 404));
    const client = view(<><TemplateCatalog principal={principal} inboxId="inbox-b" conversationId="conv-b" connectorType="meta_cloud" version={4} /><ConsentForm principal={principal} inboxId="inbox-b" contactId="contact-a" /></>);
    expect(await screen.findByText(/Boas-vindas/)).toBeInTheDocument();
    await user.type(screen.getByLabelText("Finalidade do consentimento"), "atendimento");
    await user.type(screen.getByLabelText("Origem"), "painel");
    await user.type(screen.getByLabelText("Evidência"), "evento-1");
    await user.click(screen.getByRole("button", { name: "Registrar consentimento" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Registrar consentimento" })).not.toBeInTheDocument());
    expect(screen.getAllByText("Não encontrado.")).toHaveLength(2);
    expect(client.getQueryData(policyKey(principal, "inbox-b", "templates"))).toBeUndefined();
    expect(screen.queryByText(/Boas-vindas/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Colocar template na fila" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Registrar consentimento" })).not.toBeInTheDocument();
  });
});
