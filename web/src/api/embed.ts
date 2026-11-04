import { currentCsrfToken } from "@/api/client";
import { PanelRequestError, problemMessage } from "@/api/problems";

export type EmbedContext = {
  type: "chatisp.embed.context";
  v: 1;
  conversation_id: string;
  protocol: string;
  sector_id: string;
  inbox_id: string;
};

export type EmbedSettings = {
  configured: boolean;
  version: number;
  allowed_origin: string;
  panel_url: string;
  embeddable: boolean;
};

export type EmbedView = {
  embed: boolean;
  url?: string;
  origin?: string;
  context?: EmbedContext;
};

async function panelJSON<T>(method: "GET" | "PUT", path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  const headers = new Headers({ Accept: "application/json" });
  if (body !== undefined) headers.set("Content-Type", "application/json");
  if (method !== "GET") {
    const csrf = currentCsrfToken();
    if (csrf) headers.set("X-CSRF-Token", csrf);
  }
  const request = new Request(new URL(path, window.location.origin), {
    method,
    credentials: "include",
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  const response = await fetch(request);
  const payload: unknown = await response.json().catch(() => null);
  if (!response.ok) throw new PanelRequestError(problemMessage(payload, response.status), response.status);
  return payload as T;
}

export function getEmbedSettings(signal?: AbortSignal): Promise<EmbedSettings> {
  return panelJSON("GET", "/api/panel/embed", undefined, signal);
}

export function saveEmbedSettings(allowedOrigin: string, panelURL: string): Promise<EmbedSettings> {
  return panelJSON("PUT", "/api/panel/embed", { allowed_origin: allowedOrigin, panel_url: panelURL });
}

export function getConversationEmbed(inboxId: string, conversationId: string, signal?: AbortSignal): Promise<EmbedView> {
  return panelJSON("GET", `/api/panel/inboxes/${inboxId}/conversations/${conversationId}/embed`, undefined, signal);
}
