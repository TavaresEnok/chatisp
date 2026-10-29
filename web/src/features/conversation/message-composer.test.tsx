import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { MessageComposer } from "@/features/conversation/message-composer";
import type { ConversationScope, SendTextResult } from "@/features/conversation/types";

const scopeA: ConversationScope = {
  tenantId: "tenant-fixture-a",
  userId: "usuario-fixture-a",
  conversationId: "conversa-fixture-a",
};

const scopeB: ConversationScope = {
  ...scopeA,
  conversationId: "conversa-fixture-b",
};

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe("compositor", () => {
  it("não diz que aceitou antes do retorno e guarda o rascunho na falha", async () => {
    const user = userEvent.setup();
    const pending = deferred<SendTextResult>();
    const onSend = vi.fn(() => pending.promise);
    const createIntentKey = vi.fn(() => "chave-falha");
    render(
      <MessageComposer scope={scopeA} onSend={onSend} createIntentKey={createIntentKey} />,
    );

    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "texto da fixture");
    await user.click(screen.getByRole("button", { name: "Enviar" }));

    expect(onSend).toHaveBeenCalledWith({
      intentKey: "chave-falha",
      text: "texto da fixture",
    });
    expect(screen.getByRole("status")).toHaveTextContent(
      "Enviando. O conector ainda não confirmou.",
    );
    expect(screen.queryByText("Mensagem aceita pelo conector.")).not.toBeInTheDocument();
    expect(field).toHaveValue("texto da fixture");

    pending.resolve({ outcome: "failed", detail: "O conector recusou a mensagem." });
    expect(await screen.findByRole("status")).toHaveTextContent(
      "O conector recusou a mensagem.",
    );
    expect(screen.queryByText("Mensagem aceita pelo conector.")).not.toBeInTheDocument();
    expect(field).toHaveValue("texto da fixture");
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });

  it("mantém o rascunho offline e reutiliza a chave da resposta perdida", async () => {
    const user = userEvent.setup();
    const onSend = vi
      .fn()
      .mockRejectedValueOnce(new Error("sem resposta"))
      .mockResolvedValueOnce({ outcome: "accepted" });
    const createIntentKey = vi.fn().mockReturnValueOnce("chave-1").mockReturnValueOnce("chave-2");
    const { rerender } = render(
      <MessageComposer
        scope={scopeA}
        offline
        onSend={onSend}
        createIntentKey={createIntentKey}
      />,
    );

    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "rascunho da fixture");
    await user.click(screen.getByRole("button", { name: "Enviar" }));

    expect(onSend).not.toHaveBeenCalled();
    expect(field).toHaveValue("rascunho da fixture");
    expect(screen.getByRole("status")).toHaveTextContent("o envio não foi confirmado");
    expect(screen.queryByText("Mensagem aceita pelo conector.")).not.toBeInTheDocument();
    expect(createIntentKey).toHaveBeenCalledTimes(1);

    rerender(
      <MessageComposer scope={scopeA} onSend={onSend} createIntentKey={createIntentKey} />,
    );
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenCalledTimes(1);
    expect(onSend).toHaveBeenCalledWith({
      intentKey: "chave-1",
      text: "rascunho da fixture",
    });
    expect(createIntentKey).toHaveBeenCalledTimes(1);
    expect(field).toHaveValue("rascunho da fixture");
    expect(screen.queryByText("Mensagem aceita pelo conector.")).not.toBeInTheDocument();

    expect(await screen.findByRole("status")).toHaveTextContent("A resposta do envio não chegou.");
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenLastCalledWith({
      intentKey: "chave-1",
      text: "rascunho da fixture",
    });
    expect(createIntentKey).toHaveBeenCalledTimes(1);

    expect(await screen.findByText("Mensagem aceita pelo conector.")).toBeInTheDocument();
    expect(screen.queryByText(/Entregue|Lida/)).not.toBeInTheDocument();
    expect(field).toHaveValue("");

    await user.type(field, "outra");
    expect(screen.queryByText("Mensagem aceita pelo conector.")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenLastCalledWith({ intentKey: "chave-2", text: "outra" });
  });

  it("usa outra chave na nova decisão e limpa a instância ao trocar o escopo", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn().mockResolvedValue({ outcome: "lost" });
    const createIntentKey = vi
      .fn()
      .mockReturnValueOnce("chave-1")
      .mockReturnValueOnce("chave-2")
      .mockReturnValueOnce("chave-3")
      .mockReturnValueOnce("chave-4");
    const { rerender } = render(
      <MessageComposer scope={scopeA} onSend={onSend} createIntentKey={createIntentKey} />,
    );

    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "mesma intenção");
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(await screen.findByRole("button", { name: "Enviar como nova mensagem" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Enviar como nova mensagem" }));
    expect(onSend).toHaveBeenLastCalledWith({
      intentKey: "chave-2",
      text: "mesma intenção",
    });
    expect(field).toHaveValue("mesma intenção");

    rerender(
      <MessageComposer
        scope={{ ...scopeA, tenantId: "tenant-fixture-b" }}
        onSend={onSend}
        createIntentKey={createIntentKey}
      />,
    );
    expect(screen.getByRole("textbox", { name: "Mensagem" })).toHaveValue("");
    expect(screen.queryByRole("status")).not.toBeInTheDocument();

    rerender(
      <MessageComposer scope={scopeB} offline onSend={onSend} createIntentKey={createIntentKey} />,
    );
    await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "outro escopo");
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(screen.getByRole("textbox", { name: "Mensagem" })).toHaveValue("outro escopo");
    expect(onSend).toHaveBeenCalledTimes(2);

    rerender(
      <MessageComposer
        scope={{ ...scopeB, userId: "usuario-fixture-b" }}
        onSend={onSend}
        createIntentKey={createIntentKey}
      />,
    );
    expect(screen.getByRole("textbox", { name: "Mensagem" })).toHaveValue("");
    await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "depois da troca");
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenLastCalledWith({
      intentKey: "chave-4",
      text: "depois da troca",
    });
    expect(localStorage.length).toBe(0);
    expect(sessionStorage.length).toBe(0);
  });

  it("reutiliza a chave quando o resultado fica incerto e cria outra na nova decisão", async () => {
    const user = userEvent.setup();
    const onSend = vi
      .fn()
      .mockResolvedValue({ outcome: "uncertain", detail: "O conector não confirmou." });
    const createIntentKey = vi.fn().mockReturnValueOnce("chave-1").mockReturnValueOnce("chave-2");
    render(
      <MessageComposer scope={scopeA} onSend={onSend} createIntentKey={createIntentKey} />,
    );

    const field = screen.getByRole("textbox", { name: "Mensagem" });
    await user.type(field, "texto incerto");
    await user.click(screen.getByRole("button", { name: "Enviar" }));

    expect(await screen.findByRole("status")).toHaveTextContent("O conector não confirmou.");
    expect(field).toHaveValue("texto incerto");
    expect(screen.queryByText("Mensagem aceita pelo conector.")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenLastCalledWith({ intentKey: "chave-1", text: "texto incerto" });
    expect(createIntentKey).toHaveBeenCalledTimes(1);

    expect(
      await screen.findByRole("button", { name: "Enviar como nova mensagem" }),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Enviar como nova mensagem" }));
    expect(onSend).toHaveBeenLastCalledWith({ intentKey: "chave-2", text: "texto incerto" });
    expect(createIntentKey).toHaveBeenCalledTimes(2);
    expect(field).toHaveValue("texto incerto");
    expect(screen.queryByText("Mensagem aceita pelo conector.")).not.toBeInTheDocument();
  });
});
