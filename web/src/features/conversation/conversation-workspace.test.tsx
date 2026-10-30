import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ConversationWorkspace } from "@/features/conversation/conversation-workspace";
import type { ConversationWorkspaceProps } from "@/features/conversation/conversation-workspace";
import type { SendTextResult, TextTimelineEntry } from "@/features/conversation/types";
import type {
  AttendanceActionResult,
  AttendanceScope,
  ConversationCard,
} from "@/features/conversation/workspace-types";

const scope: AttendanceScope = {
  tenantId: "tenant-fixture-a",
  userId: "usuario-fixture-a",
};

function card(overrides: Partial<ConversationCard> = {}): ConversationCard {
  return {
    id: "conversa-a",
    protocol: "protocolo-a",
    contactLabel: "Cliente da fixture",
    phase: "open",
    assigneeLabel: "",
    ...overrides,
  };
}

function textEntry(id: string, text: string, author: string): TextTimelineEntry {
  return { kind: "text", id, author, text };
}

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function mockViewport() {
  const prototype = HTMLElement.prototype;
  const height = Object.getOwnPropertyDescriptor(prototype, "clientHeight");
  const width = Object.getOwnPropertyDescriptor(prototype, "clientWidth");
  Object.defineProperty(prototype, "clientHeight", {
    configurable: true,
    get: () => 480,
  });
  Object.defineProperty(prototype, "clientWidth", {
    configurable: true,
    get: () => 320,
  });
  return () => {
    if (height) Object.defineProperty(prototype, "clientHeight", height);
    else Reflect.deleteProperty(prototype, "clientHeight");
    if (width) Object.defineProperty(prototype, "clientWidth", width);
    else Reflect.deleteProperty(prototype, "clientWidth");
  };
}

describe("workspace de atendimento", () => {
  let restore = () => undefined as void;

  afterEach(() => {
    restore();
    restore = () => undefined;
  });

  it("atalho de próxima conversa move foco e mantém navegação por setas", async () => {
    const user = userEvent.setup();
    const conversations = [card(), card({ id: "b", protocol: "b" }), card({ id: "c", protocol: "c" })];
    const onSelect = vi.fn();
    render(<ConversationWorkspace scope={scope} listStatus="ready" conversations={conversations} detailConversationId={null} detailStatus="ready" entries={[]} onSelect={onSelect} onAssume={vi.fn()} onSend={vi.fn()} onClose={vi.fn()} onFarewell={vi.fn()} />);
    const options = screen.getAllByRole("option");
    await user.click(options[0]!);
    await user.keyboard("{Control>}{Alt>}j{/Alt}{/Control}");
    expect(options[1]).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(options[2]).toHaveFocus();
    expect(onSelect).toHaveBeenLastCalledWith("c");
    screen.getByRole("button", { name: "Negrito" }).focus();
    await user.keyboard("{Control>}{Alt>}j{/Alt}{/Control}");
    expect(options[0]).toHaveFocus();
    expect(onSelect).toHaveBeenLastCalledWith("conversa-a");
  });

  it("seleciona, assume, envia com status local e encerra com os campos obrigatórios", async () => {
    restore = mockViewport();
    const user = userEvent.setup();
    const assumePending = deferred<AttendanceActionResult>();
    const sendPending = deferred<SendTextResult>();
    const onSelect = vi.fn();
    const onAssume = vi
      .fn()
      .mockImplementationOnce(() => assumePending.promise)
      .mockResolvedValueOnce({ outcome: "done" });
    const onSend = vi
      .fn()
      .mockRejectedValueOnce(new Error("sem resposta"))
      .mockImplementationOnce(() => sendPending.promise);
    const onClose = vi
      .fn()
      .mockResolvedValueOnce({
        outcome: "conflict",
        detail: "A versão da conversa mudou.",
      })
      .mockResolvedValueOnce({ outcome: "done", farewellFailed: true });
    const onFarewell = vi.fn();
    const createIntentKey = vi
      .fn()
      .mockReturnValueOnce("chave-envio")
      .mockReturnValueOnce("chave-encerramento")
      .mockReturnValueOnce("chave-despedida")
      .mockReturnValueOnce("novo-encerramento")
      .mockReturnValueOnce("nova-despedida");
    const bait = card({ id: "conversa-falsa", protocol: "protocolo-falso" });
    const first = card();
    const second = card({
      id: "conversa-b",
      protocol: "protocolo-b",
      contactLabel: "Outro cliente da fixture",
    });
    const received = textEntry(
      "entrada-a",
      "mensagem recebida da fixture",
      "Cliente da fixture",
    );
    const otherReceived = textEntry(
      "entrada-b",
      "mensagem da outra fixture",
      "Outro cliente da fixture",
    );

    function view(
      overrides: Partial<ConversationWorkspaceProps> = {},
    ): ConversationWorkspaceProps {
      return {
        scope,
        listStatus: "ready",
        conversations: [first, second],
        detailConversationId: null,
        detailStatus: "ready",
        entries: [],
        createIntentKey,
        onSelect,
        onAssume,
        onSend,
        onClose,
        onFarewell,
        ...overrides,
      };
    }

    const { rerender } = render(<ConversationWorkspace {...view({ listStatus: "loading", conversations: [bait] })} />);

    expect(screen.getByRole("status")).toHaveTextContent("Carregando as conversas.");
    expect(screen.queryByText(/protocolo-falso/)).not.toBeInTheDocument();
    expect(screen.queryByRole("option")).not.toBeInTheDocument();

    rerender(<ConversationWorkspace {...view({ listStatus: "error", conversations: [bait] })} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Nenhuma conversa foi inventada.");
    expect(screen.queryByText(/protocolo-falso/)).not.toBeInTheDocument();

    rerender(<ConversationWorkspace {...view({ conversations: [] })} />);
    expect(screen.getByText("Nenhuma conversa nesta caixa.")).toBeInTheDocument();
    expect(screen.getByText("Selecione uma conversa para ver o protocolo e o histórico.")).toBeInTheDocument();

    rerender(<ConversationWorkspace {...view()} />);
    const optionA = screen.getByRole("option", { name: /Protocolo protocolo-a/ });
    expect(optionA).toHaveTextContent("Aberta");
    expect(optionA).toHaveAccessibleName(/Sem responsável/);
    await user.click(optionA);
    expect(onSelect).toHaveBeenCalledWith("conversa-a");
    expect(screen.getByText("Carregando o histórico.")).toBeInTheDocument();
    expect(screen.queryByText("mensagem recebida da fixture")).not.toBeInTheDocument();

    rerender(
      <ConversationWorkspace
        {...view({ detailConversationId: "conversa-a", entries: [received] })}
      />,
    );
    expect(screen.getByRole("heading", { name: "Protocolo protocolo-a" })).toBeInTheDocument();
    expect(screen.getByText("mensagem recebida da fixture")).toBeInTheDocument();

    const message = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(message, "rascunho a");
    await user.click(screen.getByRole("option", { name: /Protocolo protocolo-a/ }));
    await user.keyboard("{ArrowDown}");
    expect(onSelect).toHaveBeenCalledWith("conversa-b");
    expect(screen.queryByText("mensagem recebida da fixture")).not.toBeInTheDocument();

    rerender(
      <ConversationWorkspace
        {...view({ detailConversationId: "conversa-b", entries: [otherReceived] })}
      />,
    );
    expect(screen.getByText("mensagem da outra fixture")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Mensagem" })).toHaveValue("");
    expect(screen.queryByText("mensagem recebida da fixture")).not.toBeInTheDocument();

    await user.click(screen.getByRole("option", { name: /Protocolo protocolo-b/ }));
    await user.keyboard("{ArrowUp}");
    rerender(
      <ConversationWorkspace
        {...view({ detailConversationId: "conversa-a", entries: [received] })}
      />,
    );
    expect(screen.getByRole("textbox", { name: "Mensagem" })).toHaveValue("rascunho a");
    expect(screen.queryByText("mensagem da outra fixture")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Assumir" }));
    expect(screen.getByRole("status")).toHaveTextContent("Assumindo a conversa.");
    expect(screen.getByRole("button", { name: "Assumir" })).toBeDisabled();

    await user.click(screen.getByRole("option", { name: /Protocolo protocolo-a/ }));
    await user.keyboard("{ArrowDown}");
    expect(onSelect).toHaveBeenCalledWith("conversa-b");
    rerender(
      <ConversationWorkspace
        {...view({ detailConversationId: "conversa-b", entries: [otherReceived] })}
      />,
    );
    expect(screen.getByRole("heading", { name: "Protocolo protocolo-b" })).toBeInTheDocument();
    expect(screen.queryByText("Assumindo a conversa.")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Assumir" })).toBeEnabled();
    await act(async () => {
      assumePending.resolve({
        outcome: "conflict",
        detail: "Outra pessoa ficou com esta conversa.",
      });
      await assumePending.promise;
    });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.queryByText("Outra pessoa ficou com esta conversa.")).not.toBeInTheDocument();
    expect(screen.queryByText("Assumindo a conversa.")).not.toBeInTheDocument();

    await user.click(screen.getByRole("option", { name: /Protocolo protocolo-b/ }));
    await user.keyboard("{ArrowUp}");
    rerender(
      <ConversationWorkspace
        {...view({ detailConversationId: "conversa-a", entries: [received] })}
      />,
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Outra pessoa ficou com esta conversa.",
    );

    await user.click(screen.getByRole("button", { name: "Assumir" }));
    expect(onAssume).toHaveBeenCalledTimes(2);
    expect(onAssume).toHaveBeenLastCalledWith("conversa-a");
    const assumed = card({ assigneeLabel: "Ana da fixture" });
    rerender(
      <ConversationWorkspace
        {...view({
          conversations: [assumed, second],
          detailConversationId: "conversa-a",
          entries: [received],
        })}
      />,
    );
    expect(screen.getByText("Responsável: Ana da fixture")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();

    await user.clear(screen.getByRole("textbox", { name: "Mensagem" }));
    await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "texto da fixture");
    await user.click(screen.getByRole("button", { name: /^Enviar$/ }));
    expect(onSend).toHaveBeenCalledWith("conversa-a", {
      intentKey: "chave-envio",
      text: "texto da fixture",
    });
    expect(await screen.findByRole("status")).toHaveTextContent(
      "A resposta do envio não chegou.",
    );
    expect(screen.queryByText("Mensagem aceita pelo conector.")).not.toBeInTheDocument();
    expect(screen.queryByText(/Entregue|Lida/)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /^Enviar$/ }));
    expect(onSend).toHaveBeenLastCalledWith("conversa-a", {
      intentKey: "chave-envio",
      text: "texto da fixture",
    });
    expect(createIntentKey).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("status")).toHaveTextContent(
      "Enviando. O conector ainda não confirmou.",
    );
    sendPending.resolve({ outcome: "accepted" });
    expect(await screen.findByText("Mensagem aceita pelo conector.")).toBeInTheDocument();
    expect(screen.queryByText(/Entregue ao destinatário|Lida pelo destinatário/)).not.toBeInTheDocument();

    rerender(
      <ConversationWorkspace
        {...view({
          conversations: [assumed, second],
          detailConversationId: "conversa-a",
          entries: [
            received,
            {
              kind: "text",
              id: "saida-a",
              author: "Ana da fixture",
              text: "texto da fixture",
              localState: "accepted",
            },
          ],
        })}
      />,
    );
    const accepted = screen.getByRole("listitem", { name: /texto da fixture/ });
    expect(within(accepted).getByText("Aceita pelo conector")).toHaveAttribute(
      "data-local-state",
      "accepted",
    );
    expect(within(accepted).queryByText("Entregue ao destinatário")).not.toBeInTheDocument();
    expect(within(accepted).queryByText("Lida pelo destinatário")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Encerrar com motivo" }));
    const dialog = screen.getByRole("dialog", { name: "Encerrar com motivo" });
    expect(within(dialog).getByRole("button", { name: /^Encerrar$/ })).toBeDisabled();
    await user.type(within(dialog).getByRole("textbox", { name: "Motivo" }), "Lentidão");
    expect(within(dialog).getByRole("button", { name: /^Encerrar$/ })).toBeDisabled();
    await user.selectOptions(within(dialog).getByRole("combobox", { name: "Resultado" }), "resolved");
    await user.type(
      within(dialog).getByRole("textbox", { name: "Síntese" }),
      "Orientações de reinício foram suficientes.",
    );
    expect(within(dialog).getByRole("button", { name: /^Encerrar$/ })).toBeEnabled();
    await user.type(
      within(dialog).getByRole("textbox", { name: "Despedida" }),
      "Vou encerrar por aqui.",
    );
    await user.click(within(dialog).getByRole("button", { name: /^Encerrar$/ }));
    expect(onClose).toHaveBeenCalledWith({
      conversationId: "conversa-a",
      reason: "Lentidão",
      result: "resolved",
      synthesis: "Orientações de reinício foram suficientes.",
      clientRequestId: "chave-encerramento",
      farewellBody: "Vou encerrar por aqui.",
      farewellRequestId: "chave-despedida",
    });
    expect(onFarewell).not.toHaveBeenCalled();
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "A versão da conversa mudou.",
    );

    await user.click(within(dialog).getByRole("button", { name: /^Encerrar$/ }));
    expect(onClose).toHaveBeenCalledTimes(2);
    expect(onClose).toHaveBeenNthCalledWith(2, expect.objectContaining({
      clientRequestId: "novo-encerramento",
      farewellRequestId: "nova-despedida",
    }));
    expect(onFarewell).not.toHaveBeenCalled();
    expect(await within(dialog).findByText("Atendimento encerrado, mas a despedida não foi enfileirada.")).toBeInTheDocument();
    const closedCard = card({ assigneeLabel: "Ana da fixture", phase: "closed" });
    rerender(
      <ConversationWorkspace
        {...view({
          conversations: [closedCard, second],
          detailConversationId: "conversa-a",
          entries: [received],
        })}
      />,
    );
    expect(screen.getByText("Atendimento encerrado.")).toBeInTheDocument();
    expect(screen.getByText("O compositor fica indisponível após o encerramento.")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Mensagem" })).not.toBeInTheDocument();
    expect(screen.getByText("Atendimento encerrado neste painel.")).toBeInTheDocument();
    expect(screen.queryByText(/Entregue ao destinatário|Lida pelo destinatário/)).not.toBeInTheDocument();
    expect(createIntentKey).toHaveBeenCalledTimes(5);

    rerender(
      <ConversationWorkspace
        {...view({
          conversations: [second],
          detailConversationId: null,
          entries: [],
        })}
      />,
    );
    expect(screen.getByText("Selecione uma conversa para ver o protocolo e o histórico.")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Protocolo protocolo-a" })).not.toBeInTheDocument();
    expect(screen.queryByText("mensagem recebida da fixture")).not.toBeInTheDocument();

    rerender(
      <ConversationWorkspace
        {...view({
          scope: { ...scope, tenantId: "tenant-fixture-b" },
          conversations: [first],
          detailConversationId: "conversa-a",
          entries: [received],
        })}
      />,
    );
    expect(screen.getByText("Selecione uma conversa para ver o protocolo e o histórico.")).toBeInTheDocument();
    expect(screen.queryByText("mensagem recebida da fixture")).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Mensagem" })).not.toBeInTheDocument();
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });
});
