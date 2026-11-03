import { api } from "@/api/client";
import { PanelRequestError, problemMessage } from "@/api/problems";
import type { components } from "@/api/schema";

export type CannedReply = components["schemas"]["CannedReply"];
export type CannedPage = components["schemas"]["CannedPage"];
export type CannedDraft = components["schemas"]["CannedDraft"];
export type CannedSignature = components["schemas"]["CannedSignature"];
export type CannedEdit = components["schemas"]["CannedEdit"];

function unwrap<T>(result: { data?: T; error?: unknown; response: Response }, emptyOk = false): T {
  if (result.response.ok && (emptyOk || result.data !== undefined)) {
    return result.data as T;
  }
  throw new PanelRequestError(problemMessage(result.error, result.response.status), result.response.status);
}

export async function listReplies(search: string, limit: number, offset: number, signal?: AbortSignal): Promise<CannedPage> {
  const page = unwrap(await api.GET("/api/panel/canned-replies", { params: { query: { search, limit, offset } }, signal }));
  return { replies: page.replies ?? [], has_more: page.has_more === true };
}

export async function createReply(body: CannedEdit): Promise<CannedReply> {
  return unwrap(await api.POST("/api/panel/canned-replies", { body }));
}

export async function updateReply(replyId: string, body: CannedEdit & { version: number }): Promise<CannedReply> {
  return unwrap(await api.PUT("/api/panel/canned-replies/{reply_id}", { params: { path: { reply_id: replyId } }, body }));
}

export async function deleteReply(replyId: string, version: number): Promise<void> {
  unwrap(await api.DELETE("/api/panel/canned-replies/{reply_id}", { params: { path: { reply_id: replyId }, query: { version } } }), true);
}

export async function renderReply(replyId: string, inboxId: string, conversationId: string, signal?: AbortSignal): Promise<CannedDraft> {
  const draft = unwrap(await api.POST("/api/panel/canned-replies/{reply_id}/render", {
    params: { path: { reply_id: replyId } },
    body: { inbox_id: inboxId, conversation_id: conversationId },
    signal,
  }));
  return { text: draft.text, missing: draft.missing ?? [] };
}

export async function getSignature(signal?: AbortSignal): Promise<CannedSignature> {
  const signature = unwrap(await api.GET("/api/panel/attendant-signature", { signal }));
  if (typeof signature.enabled !== "boolean" || typeof signature.prefix !== "string" || typeof signature.version !== "number") {
    throw new PanelRequestError("A assinatura recebida está incompleta.", 502);
  }
  return signature;
}

export async function saveSignature(body: CannedSignature): Promise<CannedSignature> {
  return unwrap(await api.PUT("/api/panel/attendant-signature", { body }));
}
