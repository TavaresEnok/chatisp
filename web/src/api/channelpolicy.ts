import { api } from "@/api/client";
import { PanelRequestError, problemMessage } from "@/api/problems";
import type { components } from "@/api/schema";

export type ChannelTemplate = components["schemas"]["ChannelPolicyTemplate"];
export type ConsentBody = components["schemas"]["ConsentInputBody"];
export type TemplateCommand = components["schemas"]["TemplateBody"];
export type Outbound = components["schemas"]["Outbound"];

function unwrap<T>(result: { data?: T; error?: unknown; response: Response }, emptyOk = false): T {
  if (result.response.ok && (emptyOk || result.data !== undefined)) {
    return result.data as T;
  }
  throw new PanelRequestError(problemMessage(result.error, result.response.status), result.response.status);
}

export async function listTemplates(inboxId: string, signal?: AbortSignal): Promise<ChannelTemplate[]> {
  const page = unwrap(await api.GET("/api/panel/inboxes/{inbox_id}/channel-policy/templates", {
    params: { path: { inbox_id: inboxId } },
    signal,
  }));
  return page.templates ?? [];
}

export async function syncTemplates(inboxId: string): Promise<void> {
  unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/channel-policy/templates/sync", {
    params: { path: { inbox_id: inboxId } },
  }), true);
}

export async function recordConsent(inboxId: string, contactId: string, body: ConsentBody) {
  const result = unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/channel-policy/contacts/{contact_id}/consents", {
    params: { path: { inbox_id: inboxId, contact_id: contactId } },
    body,
  }));
  return { invalidated_ids: result.invalidated_ids ?? [] };
}

/** Fila o template na operação idempotente já existente. Não abre outro envio de texto. */
export async function queueTemplate(inboxId: string, conversationId: string, body: TemplateCommand): Promise<Outbound> {
  return unwrap(await api.POST("/api/panel/inboxes/{inbox_id}/conversations/{conversation_id}/template-messages", {
    params: { path: { inbox_id: inboxId, conversation_id: conversationId } },
    body,
  }));
}
