import { registerAttachment, uploadAttachment, validateAttachment } from "@/api/attachments";
import { AttendanceError, send } from "@/api/attendance";
import type { Schema } from "@/api/attendance";
import { PanelRequestError } from "@/api/problems";
import { fileError } from "@/features/attachments/media";
import type { SendMediaCommand, SendTextCommand, SendTextResult } from "@/features/conversation/types";

const LOST_DETAIL = "A resposta do envio da mídia não chegou. A mesma tentativa será repetida.";

export function textSendBody(version: number, command: SendTextCommand): Schema["SendBody"] {
  return {
    expected_version: version,
    client_request_id: command.intentKey,
    body: command.text,
    ...(command.replyToMessageId ? { reply_to_message_id: command.replyToMessageId } : {}),
  };
}

export function mediaSendBody(version: number, command: SendMediaCommand, attachmentId: string): Schema["SendBody"] {
  return {
    expected_version: version,
    client_request_id: command.intentKey,
    body: command.caption,
    attachment_id: attachmentId,
    ...(command.replyToMessageId ? { reply_to_message_id: command.replyToMessageId } : {}),
  };
}

export function outboundResult(state: string): SendTextResult {
  if (state === "uncertain") return { outcome: "uncertain", detail: "O resultado do envio é incerto." };
  if (state === "failed") return { outcome: "failed", detail: "O envio falhou." };
  if (state === "cancelled") return { outcome: "failed", detail: "O envio foi cancelado." };
  if (["queued", "sending", "retrying", "accepted", "sent"].includes(state)) return { outcome: "accepted" };
  return { outcome: "uncertain", detail: "Estado do envio desconhecido." };
}

export async function deliverOutboundMedia(input: {
  inboxId: string;
  conversationId: string;
  version: number;
  command: SendMediaCommand;
}): Promise<SendTextResult> {
  const problem = fileError(input.command.file);
  if (problem) return { outcome: "failed", detail: problem };
  try {
    let record = await registerAttachment(input.inboxId, input.conversationId, {
      request_id: input.command.intentKey,
      name: input.command.file.name,
      bytes: input.command.file.size,
    });
    if (record.state === "upload") {
      await uploadAttachment(input.inboxId, input.conversationId, record.id, input.command.file, () => undefined);
      record = await validateAttachment(input.inboxId, input.conversationId, record.id);
    } else if (record.state === "quarantine") {
      record = await validateAttachment(input.inboxId, input.conversationId, record.id);
    }
    if (record.state === "failed") {
      return { outcome: "failed", detail: "A validação recusou o arquivo. O original permanece sem envio." };
    }
    if (record.state !== "validated") {
      return { outcome: "failed", detail: "O anexo não ficou validado." };
    }
    const result = await send(
      input.inboxId,
      input.conversationId,
      mediaSendBody(input.version, input.command, record.id),
    );
    return outboundResult(result.state);
  } catch (error) {
    const http = httpFailure(error);
    if (http) return http;
    return { outcome: "lost" };
  }
}

export function lostMediaDetail(): string {
  return LOST_DETAIL;
}

/** A resposta não confirmou a operação. A mesma chave, o anexo e a citação continuam válidos. */
export class UnconfirmedMediaSend extends Error {
  readonly result: Extract<SendTextResult, { outcome: "lost" | "uncertain" }>;

  constructor(result: Extract<SendTextResult, { outcome: "lost" | "uncertain" }>) {
    super(result.outcome === "lost" ? LOST_DETAIL : result.detail);
    this.name = "UnconfirmedMediaSend";
    this.result = result;
  }
}

function httpFailure(error: unknown): SendTextResult | null {
  if (!(error instanceof AttendanceError) && !(error instanceof PanelRequestError)) return null;
  const detail = error.message.trim() || "O envio da mídia falhou.";
  if (error.status === 0 || error.status >= 500) return { outcome: "uncertain", detail };
  if (error.status === 409) return { outcome: "failed", detail, conflict: true };
  return { outcome: "failed", detail };
}
