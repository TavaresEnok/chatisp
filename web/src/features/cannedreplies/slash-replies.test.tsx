import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MessageComposer } from "@/features/conversation/message-composer";
import { draftKey, writeDraft } from "@/features/conversation/draft-store";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
import { CannedSettings } from "@/features/cannedreplies/canned-settings";

vi.mock("@/api/cannedreplies", () => ({
  listReplies: vi.fn(),
  renderReply: vi.fn(),
  getSignature: vi.fn(),
  createReply: vi.fn(),
  updateReply: vi.fn(),
  deleteReply: vi.fn(),
  saveSignature: vi.fn(),
}));

import { createReply, deleteReply, getSignature, listReplies, renderReply, saveSignature, updateReply, type CannedSignature } from "@/api/cannedreplies";
import { PanelRequestError } from "@/api/problems";
import { cannedKey } from "@/features/cannedreplies/cache";

const principal = {
  tenantId: "tenant-a",
  userId: "user-a",
  sessionId: "sess-a",
  email: "ana@provedor.test",
  membershipId: "mem-a",
  role: "agent",
  expiresAt: "2099-01-01",
  mfaEnrollmentRequired: false,
};

const scope = { tenantId: principal.tenantId, userId: `${principal.userId}:${principal.sessionId}`, conversationId: "conv-a" };

function renderComposer(onSend = vi.fn(), prepare?: () => void) {
  useSessionStore.getState().applySession(principal, "csrf");
  prepare?.();
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <QueryClientProvider client={client}>
      <MessageComposer scope={scope} replyInboxId="inbox-a" onSend={onSend} />
    </QueryClientProvider>,
  );
  return { onSend, client, ...view };
}

describe("atalho no compositor", () => {
  beforeEach(() => {
    resetSessionStore();
    localStorage.clear();
    vi.mocked(listReplies).mockReset();
    vi.mocked(renderReply).mockReset();
    vi.mocked(getSignature).mockReset();
    vi.mocked(listReplies).mockResolvedValue({ replies: [{ id: "reply-1", shortcode: "ola", title: "Saudação", content: "modelo", version: 1 }], has_more: false });
    vi.mocked(renderReply).mockResolvedValue({ text: "Olá Ana", missing: ["protocolo"] });
    vi.mocked(getSignature).mockResolvedValue({ enabled: true, prefix: "{{nome_atendente}}: ", version: 2 });
  });

  it("insere o texto renderizado com teclado e não envia", async () => {
    const user = userEvent.setup();
    const { onSend } = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "/ola");
    expect(await screen.findByRole("option", { name: "/ola Saudação" })).toBeInTheDocument();
    await user.keyboard("{Enter}");
    expect(renderReply).toHaveBeenCalledWith("reply-1", "inbox-a", "conv-a", expect.any(AbortSignal));
    expect(field).toHaveValue("Olá Ana");
    expect(onSend).not.toHaveBeenCalled();
    expect(screen.getByText(/Marcadores sem valor: protocolo/)).toBeInTheDocument();
    expect(screen.getByText(/entra só na aceitação/)).toBeInTheDocument();
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(onSend).toHaveBeenCalledTimes(1);
  });

  it("move a opção com setas, Home e End, e Escape só fecha a lista", async () => {
    vi.mocked(listReplies).mockResolvedValue({
      replies: [
        { id: "reply-1", shortcode: "ola", title: "Saudação", content: "modelo", version: 1 },
        { id: "reply-2", shortcode: "olamundo", title: "Longa", content: "modelo", version: 1 },
      ],
      has_more: false,
    });
    const user = userEvent.setup();
    const { onSend } = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "/ola");
    const list = await screen.findByRole("listbox", { name: "Respostas compartilhadas" });
    const activeReply = () => {
      expect(field).toHaveFocus();
      expect(list).not.toHaveFocus();
      const activeId = field.getAttribute("aria-activedescendant");
      const option = document.getElementById(activeId ?? "");
      expect(option).toHaveAttribute("aria-selected", "true");
      return option;
    };
    expect(field.getAttribute("aria-controls")).toBe(list.id);
    expect(field.getAttribute("aria-owns")).toBe(list.id);
    expect(activeReply()).toHaveAccessibleName("/ola Saudação");
    await user.keyboard("{ArrowDown}");
    expect(activeReply()).toHaveAccessibleName("/olamundo Longa");
    await user.keyboard("{Home}");
    expect(activeReply()).toHaveAccessibleName("/ola Saudação");
    await user.keyboard("{End}");
    expect(activeReply()).toHaveAccessibleName("/olamundo Longa");
    await user.keyboard("{ArrowUp}");
    expect(activeReply()).toHaveAccessibleName("/ola Saudação");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("listbox", { name: "Respostas compartilhadas" })).not.toBeInTheDocument();
    expect(field).toHaveValue("/ola");
    expect(onSend).not.toHaveBeenCalled();
    expect(renderReply).not.toHaveBeenCalled();
  });

  it("não envia com Ctrl+Enter enquanto a lista está aberta e avisa prefixo repetido", async () => {
    vi.mocked(renderReply).mockResolvedValue({ text: "ana@provedor.test: Olá", missing: [] });
    const user = userEvent.setup();
    const { onSend } = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "/ola");
    await screen.findByRole("listbox", { name: "Respostas compartilhadas" });
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(onSend).not.toHaveBeenCalled();
    expect(field).toHaveValue("ana@provedor.test: Olá");
    expect(screen.getByText(/acrescenta esse prefixo de novo/)).toBeInTheDocument();
  });

  it("não envia o prefixo repetido e preserva o texto depois da correção", async () => {
    vi.mocked(renderReply).mockResolvedValue({ text: "ana@provedor.test: Olá", missing: [] });
    const user = userEvent.setup();
    const { onSend } = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "/ola");
    await user.keyboard("{Enter}");
    expect(await screen.findByText(/acrescenta esse prefixo de novo/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    field.focus();
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(onSend).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Retirar prefixo repetido" }));
    expect(field).toHaveValue("Olá");
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenCalledTimes(1);
    expect(onSend).toHaveBeenCalledWith({ intentKey: expect.any(String), text: "Olá" });
  });

  it("descarta render atrasado se o rascunho mudou", async () => {
    let resolveRender: (value: { text: string; missing: string[] }) => void = () => undefined;
    vi.mocked(renderReply).mockReturnValue(new Promise((resolve) => {
      resolveRender = resolve;
    }));
    const user = userEvent.setup();
    renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "/ola");
    await user.keyboard("{Enter}");
    await user.type(field, " e mais");
    resolveRender({ text: "Olá Ana", missing: [] });
    expect(await screen.findByText(/não foi aplicada/)).toBeInTheDocument();
    expect(field).toHaveValue("/ola e mais");
  });

  it("revalida o prefixo do rascunho restaurado antes de aceitar de novo", async () => {
    const user = userEvent.setup();
    const prefixed = "ana@provedor.test: Olá";
    const { onSend, unmount } = renderComposer(vi.fn(), () => {
      writeDraft(draftKey(principal, scope), { draft: prefixed, pending: null, phase: "idle", notice: null });
    });
    expect(await screen.findByText(/acrescenta esse prefixo de novo/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    await user.click(screen.getByRole("textbox", { name: "Mensagem" }));
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(onSend).not.toHaveBeenCalled();
    unmount();
    const again = renderComposer();
    expect(await screen.findByText(/acrescenta esse prefixo de novo/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    expect(screen.getByRole("textbox", { name: "Mensagem" })).toHaveValue(prefixed);
    await user.click(screen.getByRole("textbox", { name: "Mensagem" }));
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(again.onSend).not.toHaveBeenCalled();
    expect(onSend).not.toHaveBeenCalled();
  });

  it("não trata falha da assinatura como ausência de prefixo", async () => {
    vi.mocked(getSignature).mockRejectedValue(new Error("assinatura indisponível"));
    vi.mocked(renderReply).mockResolvedValue({ text: "ana@provedor.test: Olá", missing: [] });
    const user = userEvent.setup();
    const inserted = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "/ola");
    await user.keyboard("{Enter}");
    expect(field).toHaveValue("ana@provedor.test: Olá");
    expect(await screen.findByText(/não confirma a ausência de prefixo/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    await user.click(field);
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(inserted.onSend).not.toHaveBeenCalled();
    inserted.unmount();

    const restored = renderComposer(vi.fn(), () => {
      writeDraft(draftKey(principal, scope), { draft: "ana@provedor.test: Olá", pending: null, phase: "idle", notice: null });
    });
    expect(await screen.findByText(/não confirma a ausência de prefixo/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    await user.click(screen.getByRole("textbox", { name: "Mensagem" }));
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(restored.onSend).not.toHaveBeenCalled();
  });

  it("repete a intenção pendente com o payload original depois de remontar", async () => {
    const user = userEvent.setup();
    const text = "ana@provedor.test: Olá";
    const { onSend } = renderComposer(vi.fn(), () => {
      writeDraft(draftKey(principal, scope), {
        draft: text,
        pending: { intentKey: "mesma-chave", text },
        phase: "uncertain",
        notice: null,
      });
    });
    expect(await screen.findByRole("button", { name: "Enviar" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenCalledTimes(1);
    expect(onSend).toHaveBeenCalledWith({ intentKey: "mesma-chave", text });
    expect(screen.queryByRole("button", { name: "Enviar como nova mensagem" })).not.toBeInTheDocument();
  });

  it("consulta a assinatura ao colar o prefixo num rascunho que abriu vazio", async () => {
    const user = userEvent.setup();
    const { onSend } = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.click(field);
    await user.paste("ana@provedor.test: Olá");
    expect(await screen.findByText(/acrescenta esse prefixo de novo/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(onSend).not.toHaveBeenCalled();
    expect(getSignature).toHaveBeenCalled();
  });

  it("revalida o início quando o texto muda depois de uma renderização sem prefixo", async () => {
    const user = userEvent.setup();
    const { onSend } = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "/ola");
    await user.keyboard("{Enter}");
    expect(field).toHaveValue("Olá Ana");
    expect(await screen.findByText(/entra só na aceitação/)).toBeInTheDocument();
    field.focus();
    (field as HTMLTextAreaElement).setSelectionRange(0, 0);
    await user.paste("ana@provedor.test: ");
    expect(field).toHaveValue("ana@provedor.test: Olá Ana");
    expect(await screen.findByText(/acrescenta esse prefixo de novo/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(onSend).not.toHaveBeenCalled();
  });

  it("revalida o prefixo quando as ferramentas alteram o início do rascunho", async () => {
    vi.mocked(getSignature).mockResolvedValue({ enabled: true, prefix: "*", version: 2 });
    const user = userEvent.setup();
    const { onSend } = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" }) as HTMLTextAreaElement;
    await user.type(field, "/ola");
    await user.keyboard("{Enter}");
    expect(field).toHaveValue("Olá Ana");
    expect(await screen.findByText(/entra só na aceitação/)).toBeInTheDocument();
    await act(async () => {
      await new Promise<void>((resolve) => {
        requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
      });
    });
    field.focus();
    field.setSelectionRange(0, field.value.length);
    await user.click(screen.getByRole("button", { name: "Negrito" }));
    expect(field).toHaveValue("*Olá Ana*");
    expect(await screen.findByText(/acrescenta esse prefixo de novo/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(onSend).not.toHaveBeenCalled();
  });

  it("não envia quando a assinatura responde depois do desmonte e da troca de sessão", async () => {
    const pending = deferredSignature();
    vi.mocked(getSignature).mockImplementation(() => pending.promise);
    const user = userEvent.setup();
    const { onSend } = renderComposer(vi.fn(), () => {
      writeDraft(draftKey(principal, scope), { draft: "ana@provedor.test: Olá", pending: null, phase: "idle", notice: null });
    });
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.click(field);
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(onSend).not.toHaveBeenCalled();
    await act(async () => {
      useSessionStore.getState().applySession({ ...principal, sessionId: "sess-b", membershipId: "mem-b" }, "csrf-nova");
    });
    pending.resolve({ enabled: true, prefix: "{{nome_atendente}}: ", version: 2 });
    await act(async () => {
      await pending.promise;
    });
    expect(onSend).not.toHaveBeenCalled();
  });

  it("não aceita rascunho restaurado nem Ctrl+Enter quando a assinatura HTTP 200 vem incompleta", async () => {
    const incomplete = new PanelRequestError("A assinatura recebida está incompleta.", 502);
    vi.mocked(getSignature).mockRejectedValue(incomplete);
    const user = userEvent.setup();
    const restored = renderComposer(vi.fn(), () => {
      writeDraft(draftKey(principal, scope), { draft: "ana@provedor.test: Olá", pending: null, phase: "idle", notice: null });
    });
    expect(await screen.findByText(/não confirma a ausência de prefixo/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    await user.click(screen.getByRole("textbox", { name: "Mensagem" }));
    await user.keyboard("{Control>}{Enter}{/Control}");
    await waitFor(() => expect(vi.mocked(getSignature).mock.calls.length).toBeGreaterThanOrEqual(2));
    await act(async () => {
      await Promise.resolve();
    });
    expect(restored.onSend).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    restored.unmount();

    const pending = [deferredRejection(), deferredRejection()];
    let calls = 0;
    vi.mocked(getSignature).mockImplementation(() => {
      const slot = pending[Math.min(calls, pending.length - 1)] ?? pending[0];
      calls += 1;
      return slot?.promise ?? Promise.reject(incomplete);
    });
    const sending = renderComposer(vi.fn(), () => {
      writeDraft(draftKey(principal, scope), { draft: "ana@provedor.test: Olá", pending: null, phase: "idle", notice: null });
    });
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.click(field);
    await user.keyboard("{Control>}{Enter}{/Control}");
    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
    expect(sending.onSend).not.toHaveBeenCalled();
    pending[1]?.reject(incomplete);
    pending[0]?.reject(new DOMException("aborted", "AbortError"));
    await act(async () => {
      await pending[1]?.promise.catch(() => undefined);
      await pending[0]?.promise.catch(() => undefined);
    });
    expect(sending.onSend).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    expect(screen.getByText(/não confirma a ausência de prefixo/)).toBeInTheDocument();
  });

  it("envia com assinatura válida desligada e repete intenção pendente se o corpo vier incompleto", async () => {
    vi.mocked(getSignature).mockResolvedValue({ enabled: false, prefix: "{{nome_atendente}}: ", version: 4 });
    const user = userEvent.setup();
    const text = "ana@provedor.test: Olá";
    const allowed = renderComposer(vi.fn(), () => {
      writeDraft(draftKey(principal, scope), { draft: text, pending: null, phase: "idle", notice: null });
    });
    await waitFor(() => expect(screen.getByRole("button", { name: "Enviar" })).toBeEnabled());
    await user.click(screen.getByRole("textbox", { name: "Mensagem" }));
    await user.keyboard("{Control>}{Enter}{/Control}");
    expect(allowed.onSend).toHaveBeenCalledTimes(1);
    expect(allowed.onSend).toHaveBeenCalledWith({ intentKey: expect.any(String), text });
    allowed.unmount();

    vi.mocked(getSignature).mockRejectedValue(new PanelRequestError("A assinatura recebida está incompleta.", 502));
    const replay = renderComposer(vi.fn(), () => {
      writeDraft(draftKey(principal, scope), {
        draft: text,
        pending: { intentKey: "mesma-chave", text },
        phase: "uncertain",
        notice: null,
      });
    });
    expect(await screen.findByText(/não confirma a ausência de prefixo/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Enviar como nova mensagem" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(replay.onSend).toHaveBeenCalledTimes(1);
    expect(replay.onSend).toHaveBeenCalledWith({ intentKey: "mesma-chave", text });
  });

  it("invalida a assinatura já carregada quando a consulta da inserção falha", async () => {
    let calls = 0;
    vi.mocked(getSignature).mockImplementation(() => {
      calls += 1;
      if (calls === 1) return Promise.resolve({ enabled: false, prefix: "{{nome_atendente}}: ", version: 1 });
      return Promise.reject(new Error("assinatura indisponível"));
    });
    vi.mocked(renderReply).mockResolvedValue({ text: "ana@provedor.test: Olá", missing: [] });
    const user = userEvent.setup();
    const { onSend } = renderComposer(vi.fn(), () => {
      writeDraft(draftKey(principal, scope), { draft: "rascunho", pending: null, phase: "idle", notice: null });
    });
    await waitFor(() => expect(screen.getByRole("button", { name: "Enviar" })).toBeEnabled());
    expect(calls).toBe(1);
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.clear(field);
    await user.type(field, "/ola");
    await screen.findByRole("option", { name: "/ola Saudação" });
    await user.keyboard("{Enter}");
    expect(field).toHaveValue("ana@provedor.test: Olá");
    expect(await screen.findByText(/não confirma a ausência de prefixo/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    field.focus();
    await user.keyboard("{Control>}{Enter}{/Control}");
    await act(async () => {
      await Promise.resolve();
    });
    expect(onSend).not.toHaveBeenCalled();
    expect(calls).toBeGreaterThanOrEqual(3);
    await user.type(field, "!");
    expect(field).toHaveValue("ana@provedor.test: Olá!");
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    expect(screen.getByText(/não confirma a ausência de prefixo/)).toBeInTheDocument();
    await user.keyboard("{Control>}{Enter}{/Control}");
    await act(async () => {
      await Promise.resolve();
    });
    expect(onSend).not.toHaveBeenCalled();
  });

  it.each([403, 404])("render HTTP %s depois da lista revoga o catálogo e ignora a lista tardia", async (status) => {
    const message = status === 403 ? "Acesso negado." : "Não encontrado.";
    vi.mocked(renderReply).mockRejectedValue(new PanelRequestError(message, status));
    let resolveLate: (value: { replies: { id: string; shortcode: string; title: string; content: string; version: number }[]; has_more: boolean }) => void = () => undefined;
    const user = userEvent.setup();
    const { client } = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "/ola");
    expect(await screen.findByRole("option", { name: "/ola Saudação" })).toBeInTheDocument();
    expect(cannedQueries(client).some((query) => query.state.data !== undefined)).toBe(true);
    const listed = vi.mocked(listReplies).mock.calls.length;
    vi.mocked(listReplies).mockImplementation(() => new Promise((resolve) => {
      resolveLate = resolve;
    }));
    void client.invalidateQueries({ queryKey: cannedKey(principal, "slash", "ola") });
    await waitFor(() => expect(vi.mocked(listReplies).mock.calls.length).toBeGreaterThan(listed));
    await user.keyboard("{Enter}");
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "/ola Saudação" })).not.toBeInTheDocument();
    expect(field).toHaveValue("/ola");
    await act(async () => {
      resolveLate({ replies: [{ id: "late", shortcode: "ola", title: "Segredo tardio", content: "vazou", version: 9 }], has_more: false });
      await Promise.resolve();
    });
    expect(field).toHaveValue("/ola");
    expect(screen.queryByRole("option", { name: "/ola Saudação" })).not.toBeInTheDocument();
    expect(screen.queryByText(/Segredo tardio/)).not.toBeInTheDocument();
    expect(screen.queryByText("vazou")).not.toBeInTheDocument();
    expect(cannedQueries(client).every((query) => query.state.data === undefined)).toBe(true);
  });

  it.each([403, 404])("assinatura HTTP %s depois da lista revoga o catálogo e não insere o render tardio", async (status) => {
    const message = status === 403 ? "Acesso negado." : "Não encontrado.";
    let calls = 0;
    let rejectSignature: (error: unknown) => void = () => undefined;
    vi.mocked(getSignature).mockImplementation(() => {
      calls += 1;
      if (calls === 1) return Promise.resolve({ enabled: true, prefix: "{{nome_atendente}}: ", version: 2 });
      return new Promise<CannedSignature>((_resolve, reject) => {
        rejectSignature = reject;
      });
    });
    let resolveRender: (value: { text: string; missing: string[] }) => void = () => undefined;
    vi.mocked(renderReply).mockImplementation(() => new Promise((resolve) => {
      resolveRender = resolve;
    }));
    const user = userEvent.setup();
    const { client } = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "/ola");
    expect(await screen.findByRole("option", { name: "/ola Saudação" })).toBeInTheDocument();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
    rejectSignature(new PanelRequestError(message, status));
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "/ola Saudação" })).not.toBeInTheDocument();
    expect(field).toHaveValue("/ola");
    await act(async () => {
      resolveRender({ text: "Texto tardio", missing: [] });
      await Promise.resolve();
    });
    expect(field).toHaveValue("/ola");
    expect(screen.queryByDisplayValue("Texto tardio")).not.toBeInTheDocument();
    expect(screen.queryByText(/entra só na aceitação/)).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "/ola Saudação" })).not.toBeInTheDocument();
    expect(cannedQueries(client).every((query) => query.state.data === undefined)).toBe(true);
  });

  it.each([403, 404])("leitura direta da assinatura HTTP %s depois da lista revoga e descarta o render em voo", async (status) => {
    const message = status === 403 ? "Acesso negado." : "Não encontrado.";
    const first = deferredRejection();
    const second = deferredSignature();
    let calls = 0;
    vi.mocked(getSignature).mockImplementation(() => {
      calls += 1;
      return calls === 1 ? first.promise : second.promise;
    });
    let resolveRender: (value: { text: string; missing: string[] }) => void = () => undefined;
    vi.mocked(renderReply).mockImplementation(() => new Promise((resolve) => {
      resolveRender = resolve;
    }));
    const user = userEvent.setup();
    const { client } = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "/ola");
    expect(await screen.findByRole("option", { name: "/ola Saudação" })).toBeInTheDocument();
    expect(cannedQueries(client).some((query) => query.state.data !== undefined)).toBe(true);
    await user.keyboard("{Enter}");
    await waitFor(() => expect(renderReply).toHaveBeenCalled());
    first.reject(new PanelRequestError(message, status));
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "/ola Saudação" })).not.toBeInTheDocument();
    await act(async () => {
      resolveRender({ text: "Texto tardio", missing: [] });
      second.resolve({ enabled: true, prefix: "{{nome_atendente}}: ", version: 4 });
      await second.promise;
    });
    expect(field).toHaveValue("/ola");
    expect(screen.queryByDisplayValue("Texto tardio")).not.toBeInTheDocument();
    expect(screen.queryByText(/entra só na aceitação/)).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "/ola Saudação" })).not.toBeInTheDocument();
    expect(cannedQueries(client).every((query) => query.state.data === undefined)).toBe(true);
  });

  it.each([403, 404])("render HTTP 500 antes da assinatura HTTP %s revoga o escopo e invalida a assinatura conhecida", async (status) => {
    const message = status === 403 ? "Acesso negado." : "Não encontrado.";
    const signatureGate = deferredRejection();
    let calls = 0;
    vi.mocked(getSignature).mockImplementation(() => {
      calls += 1;
      if (calls === 1) return Promise.resolve({ enabled: true, prefix: "{{nome_atendente}}: ", version: 2 });
      return signatureGate.promise;
    });
    let rejectRender: (error: unknown) => void = () => undefined;
    vi.mocked(renderReply).mockImplementation(() => new Promise((_resolve, reject) => {
      rejectRender = reject;
    }));
    const user = userEvent.setup();
    const { client, onSend } = renderComposer();
    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "/ola");
    expect(await screen.findByRole("option", { name: "/ola Saudação" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByRole("button", { name: "Enviar" })).toBeEnabled());
    expect(cannedQueries(client).some((query) => query.state.data !== undefined)).toBe(true);
    const listed = vi.mocked(listReplies).mock.calls.length;
    let resolveLate: (value: { replies: { id: string; shortcode: string; title: string; content: string; version: number }[]; has_more: boolean }) => void = () => undefined;
    vi.mocked(listReplies).mockImplementation(() => new Promise((resolve) => {
      resolveLate = resolve;
    }));
    void client.invalidateQueries({ queryKey: cannedKey(principal, "slash", "ola") });
    await waitFor(() => expect(vi.mocked(listReplies).mock.calls.length).toBeGreaterThan(listed));
    await user.keyboard("{Enter}");
    await waitFor(() => expect(calls).toBeGreaterThanOrEqual(2));
    await waitFor(() => expect(renderReply).toHaveBeenCalled());
    rejectRender(new PanelRequestError("Falha ao renderizar.", 500));
    expect(await screen.findByText("Falha ao renderizar.")).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "/ola Saudação" })).toBeInTheDocument();
    expect(screen.queryByText(message)).not.toBeInTheDocument();
    expect(cannedQueries(client).some((query) => query.state.data !== undefined)).toBe(true);
    signatureGate.reject(new PanelRequestError(message, status));
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(screen.queryByText("Falha ao renderizar.")).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "/ola Saudação" })).not.toBeInTheDocument();
    expect(field).toHaveValue("/ola");
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    await act(async () => {
      resolveLate({ replies: [{ id: "late", shortcode: "ola", title: "Segredo tardio", content: "vazou", version: 9 }], has_more: false });
      await Promise.resolve();
    });
    expect(field).toHaveValue("/ola");
    expect(screen.queryByRole("option", { name: "/ola Saudação" })).not.toBeInTheDocument();
    expect(screen.queryByText(/Segredo tardio/)).not.toBeInTheDocument();
    expect(screen.queryByText("vazou")).not.toBeInTheDocument();
    expect(cannedQueries(client).every((query) => query.state.data === undefined)).toBe(true);
    await user.type(field, "!");
    expect(field).toHaveValue("/ola!");
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
    field.focus();
    await user.keyboard("{Control>}{Enter}{/Control}");
    await act(async () => {
      await Promise.resolve();
    });
    expect(onSend).not.toHaveBeenCalled();
    expect(calls).toBeGreaterThanOrEqual(3);
    expect(screen.getByRole("button", { name: "Enviar" })).toBeDisabled();
  });
});

function deferredSignature() {
  let resolve: (value: CannedSignature) => void = () => undefined;
  const promise = new Promise<CannedSignature>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function deferredRejection() {
  let reject: (error: unknown) => void = () => undefined;
  const promise = new Promise<CannedSignature>((_resolve, done) => {
    reject = done;
  });
  return { promise, reject };
}

describe("papéis do catálogo", () => {
  beforeEach(() => {
    resetSessionStore();
    vi.mocked(listReplies).mockResolvedValue({ replies: [], has_more: false });
    vi.mocked(getSignature).mockResolvedValue({ enabled: false, prefix: "{{nome_atendente}}: ", version: 0 });
  });

  it("oculta a criação para o atendente", async () => {
    const user = userEvent.setup();
    useSessionStore.getState().applySession(principal, "csrf");
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><CannedSettings principal={principal} /></QueryClientProvider>);
    expect(listReplies).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Mostrar respostas compartilhadas" }));
    expect(await screen.findByText(/consulta e usa as respostas/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Criar resposta" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Gravar assinatura" })).not.toBeInTheDocument();
  });
});

const catalog = {
  replies: [{ id: "reply-1", shortcode: "ola", title: "Saudação interna", content: "modelo", version: 1 }],
  has_more: false,
};
const admin = { ...principal, role: "admin" };

function cannedQueries(client: QueryClient) {
  return client.getQueryCache().getAll().filter((query) => query.queryKey[0] === "m4a-canned");
}

function renderCatalog() {
  const late: Array<(value: typeof catalog) => void> = [];
  let calls = 0;
  vi.mocked(listReplies).mockImplementation(() => {
    calls += 1;
    if (calls === 1) return Promise.resolve(catalog);
    return new Promise((resolve) => {
      late.push(resolve);
    });
  });
  vi.mocked(getSignature).mockResolvedValue({ enabled: false, prefix: "{{nome_atendente}}: ", version: 3 });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><CannedSettings principal={admin} /></QueryClientProvider>);
  return { client, late, calls: () => calls };
}

async function openCatalog(user: ReturnType<typeof userEvent.setup>) {
  const view = renderCatalog();
  await user.click(screen.getByRole("button", { name: "Mostrar respostas compartilhadas" }));
  await waitFor(() => expect(document.body.textContent).toContain("Saudação interna"));
  expect(await screen.findByRole("button", { name: "Gravar assinatura" })).toBeInTheDocument();
  void view.client.invalidateQueries({ queryKey: cannedKey(admin, "catalog") }).catch(() => undefined);
  await waitFor(() => expect(view.calls()).toBeGreaterThanOrEqual(2));
  return view;
}

async function expectCatalogRevoked(view: ReturnType<typeof renderCatalog>, message = "Acesso negado.") {
  expect(await screen.findByText(message)).toBeInTheDocument();
  expect(document.body.textContent).not.toContain("Saudação interna");
  expect(document.body.textContent).not.toContain("modelo");
  for (const name of ["Excluir resposta", "Editar resposta", "Criar resposta", "Gravar assinatura"]) {
    expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
  }
  expect(cannedQueries(view.client).every((query) => query.state.data === undefined)).toBe(true);
  await act(async () => {
    for (const resolve of view.late) resolve(catalog);
    await Promise.resolve();
  });
  expect(document.body.textContent).not.toContain("Saudação interna");
  expect(document.body.textContent).not.toContain("modelo");
  expect(screen.queryByRole("button", { name: "Criar resposta" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Gravar assinatura" })).not.toBeInTheDocument();
  expect(cannedQueries(view.client).every((query) => query.state.data === undefined)).toBe(true);
}

describe("revogação do catálogo", () => {
  beforeEach(() => {
    resetSessionStore();
    vi.mocked(listReplies).mockReset();
    vi.mocked(getSignature).mockReset();
    vi.mocked(createReply).mockReset();
    vi.mocked(updateReply).mockReset();
    vi.mocked(deleteReply).mockReset();
    vi.mocked(saveSignature).mockReset();
  });

  it.each([403, 404])("excluir com HTTP %s tira o catálogo e ignora a lista tardia", async (status) => {
    const user = userEvent.setup();
    vi.mocked(deleteReply).mockRejectedValue(new PanelRequestError(status === 403 ? "Acesso negado." : "Não encontrado.", status));
    const view = await openCatalog(user);
    await user.click(screen.getByRole("button", { name: "Excluir resposta" }));
    await expectCatalogRevoked(view, status === 403 ? "Acesso negado." : "Não encontrado.");
  });

  it("criar com HTTP 403 tira o catálogo e ignora a lista tardia", async () => {
    const user = userEvent.setup();
    vi.mocked(createReply).mockRejectedValue(new PanelRequestError("Acesso negado.", 403));
    const view = await openCatalog(user);
    await user.type(screen.getByRole("textbox", { name: "Atalho" }), "aviso");
    await user.type(screen.getByRole("textbox", { name: "Título" }), "Aviso");
    await user.type(screen.getByRole("textbox", { name: "Conteúdo" }), "texto");
    await user.click(screen.getByRole("button", { name: "Criar resposta" }));
    await expectCatalogRevoked(view);
  });

  it("editar com HTTP 404 tira o catálogo e ignora a lista tardia", async () => {
    const user = userEvent.setup();
    vi.mocked(updateReply).mockRejectedValue(new PanelRequestError("Não encontrado.", 404));
    const view = await openCatalog(user);
    await user.click(screen.getByRole("button", { name: "Editar resposta" }));
    const form = screen.getByRole("form", { name: "Editar resposta" });
    await user.clear(within(form).getByRole("textbox", { name: "Título" }));
    await user.type(within(form).getByRole("textbox", { name: "Título" }), "Novo título");
    await user.click(within(form).getByRole("button", { name: "Atualizar resposta" }));
    await expectCatalogRevoked(view, "Não encontrado.");
  });

  it("gravar assinatura com HTTP 403 tira o catálogo e ignora a lista tardia", async () => {
    const user = userEvent.setup();
    vi.mocked(saveSignature).mockRejectedValue(new PanelRequestError("Acesso negado.", 403));
    const view = await openCatalog(user);
    await user.click(screen.getByRole("button", { name: "Gravar assinatura" }));
    await expectCatalogRevoked(view);
  });

  it("consulta seguinte 403 tira o catálogo e ignora a assinatura tardia", async () => {
    const user = userEvent.setup();
    const lateSignature = deferredSignature();
    let calls = 0;
    vi.mocked(listReplies).mockImplementation(() => {
      calls += 1;
      if (calls === 1) return Promise.resolve(catalog);
      return Promise.reject(new PanelRequestError("Acesso negado.", 403));
    });
    vi.mocked(getSignature).mockImplementation(() => lateSignature.promise);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={client}><CannedSettings principal={admin} /></QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: "Mostrar respostas compartilhadas" }));
    await waitFor(() => expect(document.body.textContent).toContain("Saudação interna"));
    await user.type(screen.getByLabelText("Buscar respostas"), "x");
    await user.click(screen.getByRole("button", { name: "Buscar" }));
    expect(await screen.findByText("Acesso negado.")).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("Saudação interna");
    expect(document.body.textContent).not.toContain("modelo");
    expect(screen.queryByRole("button", { name: "Criar resposta" })).not.toBeInTheDocument();
    await act(async () => {
      lateSignature.resolve({ enabled: true, prefix: "{{nome_atendente}}: ", version: 9 });
      await lateSignature.promise;
    });
    expect(document.body.textContent).not.toContain("Saudação interna");
    expect(document.body.textContent).not.toContain("modelo");
    expect(screen.queryByRole("button", { name: "Gravar assinatura" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Criar resposta" })).not.toBeInTheDocument();
    expect(cannedQueries(client).every((query) => query.state.data === undefined)).toBe(true);
  });

  it("grava a assinatura no cache enquanto a instância segue a mesma", async () => {
    const user = userEvent.setup();
    const saved = { enabled: true, prefix: "{{nome_atendente}}: ", version: 4 };
    vi.mocked(saveSignature).mockResolvedValue(saved);
    const view = await openCatalog(user);
    await user.click(screen.getByRole("button", { name: "Gravar assinatura" }));
    expect(await screen.findByText(/Versão 4/)).toBeInTheDocument();
    expect(view.client.getQueryData(cannedKey(admin, "signature"))).toEqual(saved);
  });

  it.each([403, 404])("não recria a assinatura quando o PUT sucede depois da negação %s", async (status) => {
    const user = userEvent.setup();
    const message = status === 403 ? "Acesso negado." : "Não encontrado.";
    let resolveSave: (value: CannedSignature) => void = () => undefined;
    vi.mocked(saveSignature).mockImplementation(() => new Promise((resolve) => {
      resolveSave = resolve;
    }));
    vi.mocked(deleteReply).mockRejectedValue(new PanelRequestError(message, status));
    const view = await openCatalog(user);
    await user.click(screen.getByRole("button", { name: "Gravar assinatura" }));
    await waitFor(() => expect(saveSignature).toHaveBeenCalledTimes(1));
    await user.click(screen.getByRole("button", { name: "Excluir resposta" }));
    await expectCatalogRevoked(view, message);
    await act(async () => {
      resolveSave({ enabled: true, prefix: "{{nome_atendente}}: ", version: 8 });
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(view.client.getQueryData(cannedKey(admin, "signature"))).toBeUndefined();
    expect(cannedQueries(view.client).some((query) => query.state.data !== undefined)).toBe(false);
    expect(screen.queryByText("Assinatura gravada.")).not.toBeInTheDocument();
    expect(screen.queryByText(/Versão 8/)).not.toBeInTheDocument();
  });

  it("não recria a assinatura quando o PUT sucede depois de sair da seção", async () => {
    const user = userEvent.setup();
    let resolveSave: (value: CannedSignature) => void = () => undefined;
    vi.mocked(saveSignature).mockImplementation(() => new Promise((resolve) => {
      resolveSave = resolve;
    }));
    vi.mocked(listReplies).mockResolvedValue(catalog);
    vi.mocked(getSignature).mockResolvedValue({ enabled: false, prefix: "{{nome_atendente}}: ", version: 3 });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const view = render(<QueryClientProvider client={client}><CannedSettings principal={admin} /></QueryClientProvider>);
    await user.click(screen.getByRole("button", { name: "Mostrar respostas compartilhadas" }));
    expect(await screen.findByRole("button", { name: "Gravar assinatura" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Gravar assinatura" }));
    await waitFor(() => expect(saveSignature).toHaveBeenCalledTimes(1));
    view.unmount();
    await act(async () => {
      resolveSave({ enabled: true, prefix: "{{nome_atendente}}: ", version: 8 });
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(client.getQueryData(cannedKey(admin, "signature"))).toBeUndefined();
    expect(cannedQueries(client).some((query) => query.state.data !== undefined)).toBe(false);
  });

  it("não recria a assinatura quando o PUT sucede depois da troca de sessão", async () => {
    const user = userEvent.setup();
    let resolveSave: (value: CannedSignature) => void = () => undefined;
    vi.mocked(saveSignature).mockImplementation(() => new Promise((resolve) => {
      resolveSave = resolve;
    }));
    vi.mocked(listReplies).mockResolvedValue(catalog);
    vi.mocked(getSignature).mockResolvedValue({ enabled: false, prefix: "{{nome_atendente}}: ", version: 3 });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const next = { ...admin, sessionId: "sess-b" };
    const frame = (current: typeof admin) => (
      <QueryClientProvider client={client}><CannedSettings principal={current} /></QueryClientProvider>
    );
    const view = render(frame(admin));
    await user.click(screen.getByRole("button", { name: "Mostrar respostas compartilhadas" }));
    expect(await screen.findByRole("button", { name: "Gravar assinatura" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Gravar assinatura" }));
    await waitFor(() => expect(saveSignature).toHaveBeenCalledTimes(1));
    view.rerender(frame(next));
    expect(await screen.findByRole("button", { name: "Gravar assinatura" })).toBeInTheDocument();
    expect(client.getQueryData(cannedKey(next, "signature"))).toEqual({ enabled: false, prefix: "{{nome_atendente}}: ", version: 3 });
    await act(async () => {
      resolveSave({ enabled: true, prefix: "{{nome_atendente}}: ", version: 8 });
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(client.getQueryData(cannedKey(admin, "signature"))).toBeUndefined();
    expect(client.getQueryData(cannedKey(next, "signature"))).toEqual({ enabled: false, prefix: "{{nome_atendente}}: ", version: 3 });
    expect(screen.queryByText("Assinatura gravada.")).not.toBeInTheDocument();
    expect(screen.queryByText(/Versão 8/)).not.toBeInTheDocument();
  });
});
