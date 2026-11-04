import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MessageComposer } from "@/features/conversation/message-composer";
import { UnconfirmedMediaSend } from "@/features/media/send-media";
import { resetSessionStore, useSessionStore } from "@/stores/session-store";
import type { ConversationScope, SendAudioCommand, SendMediaCommand, SendTextResult } from "@/features/conversation/types";

const scope: ConversationScope = {
  tenantId: "tenant-fixture-a",
  userId: "usuario-fixture-a",
  conversationId: "conversa-fixture-a",
};

const replyId = "11111111-1111-4111-8111-111111111111";
const reply = { id: replyId, preview: "texto citado" };

function recorder() {
  const chunks = Uint8Array.of(9, 8, 7, 6);
  class FakeRecorder {
    mimeType: string;
    state = "inactive";
    ondataavailable: ((event: { data: Blob }) => void) | null = null;
    onstop: (() => void) | null = null;
    constructor(_stream: MediaStream, options?: { mimeType?: string }) {
      this.mimeType = options?.mimeType ?? "audio/webm";
    }
    start() {
      this.state = "recording";
    }
    stop() {
      this.state = "inactive";
      this.ondataavailable?.({ data: new Blob([chunks], { type: this.mimeType }) });
      this.onstop?.();
    }
    static isTypeSupported(type: string) {
      return type === "audio/webm";
    }
  }
  vi.stubGlobal("MediaRecorder", FakeRecorder);
  Object.defineProperty(navigator, "mediaDevices", {
    configurable: true,
    value: { getUserMedia: vi.fn(async () => ({ getTracks: () => [{ stop() {} }] })) },
  });
}

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe("compositor de mídia", () => {
  afterEach(() => {
    resetSessionStore();
    localStorage.clear();
  });
  it("anexa, arrasta e cola um arquivo e envia com legenda e reply_to", async () => {
    const user = userEvent.setup();
    const onSendMedia = vi.fn<(command: SendMediaCommand) => Promise<SendTextResult>>(async () => ({ outcome: "accepted" }));
    const onSend = vi.fn<(command: { intentKey: string; text: string; replyToMessageId?: string }) => Promise<SendTextResult>>(async () => ({ outcome: "accepted" }));
    render(
      <MessageComposer
        scope={scope}
        onSend={onSend}
        onSendMedia={onSendMedia}
        replyTo={reply}
        createIntentKey={() => "chave-midia"}
      />,
    );

    const file = new File([Uint8Array.of(1, 2, 3, 4)], "fachada.jpg", { type: "image/jpeg" });
    await user.upload(screen.getByLabelText("Anexar arquivo"), file);
    expect(screen.getByText("Anexo pronto: fachada.jpg")).toBeInTheDocument();
    await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "foto da fachada");
    await user.click(screen.getByRole("button", { name: "Enviar" }));

    expect(onSend).not.toHaveBeenCalled();
    expect(onSendMedia).toHaveBeenCalledWith({
      intentKey: "chave-midia",
      caption: "foto da fachada",
      file,
      replyToMessageId: replyId,
    });
    expect(await screen.findByText(/Mídia aceita pelo conector/)).toBeInTheDocument();
    expect(screen.getByText(/não confirma a entrega/)).toBeInTheDocument();

    const pasted = new File([Uint8Array.of(5)], "cola.png", { type: "image/png" });
    fireEvent.paste(screen.getByRole("textbox", { name: "Mensagem" }), {
      clipboardData: { files: [pasted], types: ["Files"] },
    });
    expect(screen.getByText("Anexo pronto: cola.png")).toBeInTheDocument();

    const dropped = new File([Uint8Array.of(6)], "arraste.png", { type: "image/png" });
    const form = screen.getByRole("form", { name: "Compositor de mensagem" });
    fireEvent.dragOver(form, { dataTransfer: { files: [dropped], types: ["Files"] } });
    fireEvent.drop(form, { dataTransfer: { files: [dropped], types: ["Files"] } });
    expect(screen.getByText("Anexo pronto: arraste.png")).toBeInTheDocument();
  });

  it("cita a mensagem no envio de texto", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn(async () => ({ outcome: "accepted" as const }));
    render(
      <MessageComposer scope={scope} onSend={onSend} replyTo={reply} createIntentKey={() => "chave-texto"} />,
    );
    await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "resposta");
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenCalledWith({ intentKey: "chave-texto", text: "resposta", replyToMessageId: replyId });
  });

  it("mostra o erro de conversão da gravação sem converter no navegador", async () => {
    const user = userEvent.setup();
    recorder();
    const onSendAudio = vi.fn<(command: SendAudioCommand) => Promise<void>>();
    onSendAudio.mockRejectedValueOnce(new Error("ffmpeg não encontrado; conversão recusada; arquivo original preservado"));
    onSendAudio.mockRejectedValueOnce(new Error("ffmpeg falhou; conversão recusada; arquivo original preservado"));
    render(
      <MessageComposer scope={scope} onSend={vi.fn()} onSendAudio={onSendAudio} createIntentKey={() => "chave-audio"} />,
    );

    await user.click(screen.getByRole("button", { name: "Gravar áudio" }));
    await user.click(screen.getByRole("button", { name: "Parar gravação" }));
    expect(screen.getByRole("status")).toHaveTextContent("sem converter");
    await user.click(screen.getByRole("button", { name: "Enviar áudio original" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("ffmpeg não encontrado; conversão recusada; arquivo original preservado");
    expect(screen.getByLabelText("Gravação de áudio")).toHaveAttribute("data-conversion-state", "refused");

    await user.click(screen.getByRole("button", { name: "Enviar áudio original" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("ffmpeg falhou; conversão recusada; arquivo original preservado");
    expect(onSendAudio).toHaveBeenCalledTimes(2);
    expect(onSendAudio.mock.calls[0]?.[0]?.mimeType).toBe("audio/webm");
    expect(onSendAudio.mock.calls[0]?.[0]?.blob).toBeInstanceOf(Blob);
  });

  it("repete a citação original do texto depois de perda, cancelamento ou troca", async () => {
    const user = userEvent.setup();
    const onSend = vi.fn(async () => ({ outcome: "lost" as const }));
    const onClearReply = vi.fn();
    const createIntentKey = vi.fn().mockReturnValueOnce("chave-1").mockReturnValueOnce("chave-2");
    const other = { id: "22222222-2222-4222-8222-222222222222", preview: "outro alvo" };
    const { rerender } = render(
      <MessageComposer scope={scope} onSend={onSend} replyTo={reply} onClearReply={onClearReply} createIntentKey={createIntentKey} />,
    );
    await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "texto citado");
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenCalledWith({ intentKey: "chave-1", text: "texto citado", replyToMessageId: replyId });
    expect(await screen.findByText(/A resposta do envio não chegou/)).toBeInTheDocument();

    rerender(<MessageComposer scope={scope} onSend={onSend} replyTo={null} onClearReply={onClearReply} createIntentKey={createIntentKey} />);
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenLastCalledWith({ intentKey: "chave-1", text: "texto citado", replyToMessageId: replyId });

    rerender(<MessageComposer scope={scope} onSend={onSend} replyTo={other} onClearReply={onClearReply} createIntentKey={createIntentKey} />);
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenLastCalledWith({ intentKey: "chave-1", text: "texto citado", replyToMessageId: replyId });
    expect(createIntentKey).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole("button", { name: "Enviar como nova mensagem" }));
    expect(onSend).toHaveBeenLastCalledWith({ intentKey: "chave-2", text: "texto citado", replyToMessageId: other.id });
    expect(createIntentKey).toHaveBeenCalledTimes(2);
  });

  it("guarda a citação do texto perdido e a reenvia depois de remontar", async () => {
    const user = userEvent.setup();
    useSessionStore.getState().applySession({
      tenantId: "tenant-a", userId: "user-a", sessionId: "sess-a", email: "ana@provedor.test",
      membershipId: "mem-a", role: "agent", expiresAt: "2099-01-01", mfaEnrollmentRequired: false,
    }, "csrf");
    const stored = { tenantId: "tenant-a", userId: "user-a:sess-a", conversationId: "conv-a" };
    const onSend = vi.fn(async () => ({ outcome: "uncertain" as const, detail: "O conector não confirmou." }));
    const createIntentKey = vi.fn(() => "chave-persistida");
    const view = render(<MessageComposer scope={stored} onSend={onSend} replyTo={reply} createIntentKey={createIntentKey} />);
    await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "texto persistido");
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(await screen.findByText("O conector não confirmou.")).toBeInTheDocument();
    expect(localStorage.length).toBeGreaterThan(0);
    view.unmount();
    render(<MessageComposer scope={stored} onSend={onSend} replyTo={null} createIntentKey={createIntentKey} />);
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSend).toHaveBeenLastCalledWith({ intentKey: "chave-persistida", text: "texto persistido", replyToMessageId: replyId });
    expect(createIntentKey).toHaveBeenCalledTimes(1);
  });

  it("não libera outro anexo enquanto o envio está em voo e ignora o resultado antigo", async () => {
    const user = userEvent.setup();
    const pending = deferred<SendTextResult>();
    const onSendMedia = vi.fn<(command: SendMediaCommand) => Promise<SendTextResult>>(() => pending.promise);
    const createIntentKey = vi.fn().mockReturnValueOnce("chave-1").mockReturnValueOnce("chave-2");
    render(<MessageComposer scope={scope} onSend={vi.fn()} onSendMedia={onSendMedia} createIntentKey={createIntentKey} />);
    const original = new File([Uint8Array.of(1, 2)], "original.jpg", { type: "image/jpeg" });
    await user.upload(screen.getByLabelText("Anexar arquivo"), original);
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSendMedia).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText("Anexar arquivo")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Remover anexo" })).toBeDisabled();

    const replacement = new File([Uint8Array.of(3)], "troca.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("Anexar arquivo"), { target: { files: [replacement] } });
    const form = screen.getByRole("form", { name: "Compositor de mensagem" });
    fireEvent.drop(form, { dataTransfer: { files: [replacement], types: ["Files"] } });
    fireEvent.click(screen.getByRole("button", { name: "Remover anexo" }));
    expect(screen.getByText("Anexo pronto: original.jpg")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSendMedia).toHaveBeenCalledTimes(1);
    expect(onSendMedia.mock.calls[0]?.[0]?.intentKey).toBe("chave-1");

    pending.resolve({ outcome: "accepted" });
    expect(await screen.findByText(/Mídia aceita pelo conector/)).toBeInTheDocument();
    expect(screen.queryByText("Anexo pronto: original.jpg")).not.toBeInTheDocument();
    expect(createIntentKey).toHaveBeenCalledTimes(1);

    const next = new File([Uint8Array.of(4)], "seguinte.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("Anexar arquivo"), { target: { files: [next] } });
    expect(screen.getByText("Anexo pronto: seguinte.png")).toBeInTheDocument();
    expect(screen.queryByText(/Mídia aceita pelo conector/)).not.toBeInTheDocument();
  });

  it("repete a mídia perdida com a mesma citação e a mesma chave", async () => {
    const user = userEvent.setup();
    const onSendMedia = vi.fn<(command: SendMediaCommand) => Promise<SendTextResult>>();
    onSendMedia.mockResolvedValueOnce({ outcome: "lost" });
    onSendMedia.mockResolvedValueOnce({ outcome: "accepted" });
    const createIntentKey = vi.fn().mockReturnValueOnce("chave-midia").mockReturnValueOnce("chave-nova");
    const { rerender } = render(
      <MessageComposer scope={scope} onSend={vi.fn()} onSendMedia={onSendMedia} replyTo={reply} createIntentKey={createIntentKey} />,
    );
    const file = new File([Uint8Array.of(1)], "fachada.jpg", { type: "image/jpeg" });
    await user.upload(screen.getByLabelText("Anexar arquivo"), file);
    await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "legenda original");
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(await screen.findByText(/A mesma tentativa será repetida/)).toBeInTheDocument();
    rerender(<MessageComposer scope={scope} onSend={vi.fn()} onSendMedia={onSendMedia} replyTo={null} createIntentKey={createIntentKey} />);
    await user.clear(screen.getByRole("textbox", { name: "Mensagem" }));
    await user.type(screen.getByRole("textbox", { name: "Mensagem" }), "legenda trocada");
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(onSendMedia).toHaveBeenLastCalledWith({
      intentKey: "chave-midia",
      caption: "legenda original",
      file,
      replyToMessageId: replyId,
    });
    expect(createIntentKey).toHaveBeenCalledTimes(1);
    expect(await screen.findByText(/Mídia aceita pelo conector/)).toBeInTheDocument();
  });

  it("grava com a resposta do compositor e repete a mesma operação se a resposta se perder", async () => {
    const user = userEvent.setup();
    recorder();
    const onSendAudio = vi.fn<(command: SendAudioCommand) => Promise<void>>();
    onSendAudio.mockRejectedValueOnce(new UnconfirmedMediaSend({ outcome: "lost" }));
    onSendAudio.mockResolvedValueOnce(undefined);
    const createIntentKey = vi.fn().mockReturnValueOnce("chave-1").mockReturnValueOnce("chave-2");
    const onClearReply = vi.fn();
    const { rerender } = render(
      <MessageComposer scope={scope} onSend={vi.fn()} onSendAudio={onSendAudio} replyTo={reply} onClearReply={onClearReply} createIntentKey={createIntentKey} />,
    );
    await user.click(screen.getByRole("button", { name: "Gravar áudio" }));
    await user.click(screen.getByRole("button", { name: "Parar gravação" }));
    await user.click(screen.getByRole("button", { name: "Enviar áudio original" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("A mesma tentativa será repetida");
    expect(onSendAudio.mock.calls[0]?.[0]?.replyToMessageId).toBe(replyId);
    expect(onSendAudio.mock.calls[0]?.[0]?.intentKey).toBe("chave-1");

    rerender(<MessageComposer scope={scope} onSend={vi.fn()} onSendAudio={onSendAudio} replyTo={null} onClearReply={onClearReply} createIntentKey={createIntentKey} />);
    await user.type(screen.getByLabelText("Responder à mensagem"), "22222222-2222-4222-8222-222222222222");
    await user.click(screen.getByRole("button", { name: "Enviar áudio original" }));
    expect(onSendAudio).toHaveBeenCalledTimes(2);
    expect(onSendAudio.mock.calls[1]?.[0]?.intentKey).toBe("chave-1");
    expect(onSendAudio.mock.calls[1]?.[0]?.replyToMessageId).toBe(replyId);
    expect(onSendAudio.mock.calls[1]?.[0]?.blob).toBe(onSendAudio.mock.calls[0]?.[0]?.blob);
    expect(createIntentKey).toHaveBeenCalledTimes(1);
    expect(await screen.findByText(/não converteu/)).toBeInTheDocument();
    expect(onClearReply).toHaveBeenCalledTimes(1);
  });

  it("abre outra chave de áudio só depois de falha definitiva", async () => {
    const user = userEvent.setup();
    recorder();
    const onSendAudio = vi.fn<(command: SendAudioCommand) => Promise<void>>(async () => {
      throw new Error("ffmpeg não encontrado; conversão recusada; arquivo original preservado");
    });
    const createIntentKey = vi.fn().mockReturnValueOnce("chave-1").mockReturnValueOnce("chave-2");
    render(<MessageComposer scope={scope} onSend={vi.fn()} onSendAudio={onSendAudio} createIntentKey={createIntentKey} />);
    await user.click(screen.getByRole("button", { name: "Gravar áudio" }));
    await user.click(screen.getByRole("button", { name: "Parar gravação" }));
    await user.click(screen.getByRole("button", { name: "Enviar áudio original" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("ffmpeg não encontrado");
    await user.click(screen.getByRole("button", { name: "Enviar áudio original" }));
    expect(onSendAudio.mock.calls[0]?.[0]?.intentKey).toBe("chave-1");
    expect(onSendAudio.mock.calls[1]?.[0]?.intentKey).toBe("chave-2");
    expect(createIntentKey).toHaveBeenCalledTimes(2);
  });

  it("não envia a gravação enquanto um anexo está em voo", async () => {
    const user = userEvent.setup();
    recorder();
    const pending = deferred<SendTextResult>();
    const onSendMedia = vi.fn<(command: SendMediaCommand) => Promise<SendTextResult>>(() => pending.promise);
    const onSendAudio = vi.fn<(command: SendAudioCommand) => Promise<void>>(async () => undefined);
    render(
      <MessageComposer scope={scope} onSend={vi.fn()} onSendMedia={onSendMedia} onSendAudio={onSendAudio} createIntentKey={() => "chave-anexo"} />,
    );
    await user.click(screen.getByRole("button", { name: "Gravar áudio" }));
    await user.click(screen.getByRole("button", { name: "Parar gravação" }));
    const file = new File([Uint8Array.of(1)], "foto.jpg", { type: "image/jpeg" });
    await user.upload(screen.getByLabelText("Anexar arquivo"), file);
    await user.click(screen.getByRole("button", { name: "Enviar" }));
    expect(screen.getByRole("button", { name: "Enviar áudio original" })).toBeDisabled();
    expect(onSendAudio).not.toHaveBeenCalled();
    pending.resolve({ outcome: "accepted" });
    expect(await screen.findByText(/Mídia aceita pelo conector/)).toBeInTheDocument();
    expect(onSendMedia).toHaveBeenCalledTimes(1);
  });
});
