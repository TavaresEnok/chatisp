import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { MessageComposer } from "@/features/conversation/message-composer";
import type { ConversationScope, SendAudioCommand } from "@/features/conversation/types";

const scope: ConversationScope = {
  tenantId: "tenant-fixture-a",
  userId: "usuario-fixture-a",
  conversationId: "conversa-fixture-a",
};

const replyId = "11111111-1111-4111-8111-111111111111";

describe("compositor de áudio", () => {
  it("envia o blob original do MediaRecorder e a citação interna", async () => {
    const user = userEvent.setup();
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
    const onSendAudio = vi.fn<(command: SendAudioCommand) => Promise<void>>(async () => undefined);
    render(
      <MessageComposer
        scope={scope}
        onSend={vi.fn()}
        onSendAudio={onSendAudio}
        createIntentKey={() => "chave-audio"}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Gravar áudio" }));
    await user.click(screen.getByRole("button", { name: "Parar gravação" }));
    expect(screen.getByRole("status")).toHaveTextContent("sem converter");
    await user.type(screen.getByLabelText("Responder à mensagem"), replyId);
    await user.click(screen.getByRole("button", { name: "Enviar áudio original" }));

    expect(onSendAudio).toHaveBeenCalledTimes(1);
    const command = onSendAudio.mock.calls[0]?.[0];
    if (!command) {
      throw new Error("áudio não enviado");
    }
    expect(command.intentKey).toBe("chave-audio");
    expect(command.mimeType).toBe("audio/webm");
    expect(command.replyToMessageId).toBe(replyId);
    expect(command.blob).toBeInstanceOf(Blob);
    expect(command.blob.size).toBe(4);
    expect(command.blob.type).toBe("audio/webm");
    expect(new Uint8Array(await command.blob.arrayBuffer())).toEqual(chunks);
    expect(screen.getByRole("status")).toHaveTextContent("não converteu");
  });
});
