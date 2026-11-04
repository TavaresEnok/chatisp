import { currentCsrfToken } from "@/api/client";
import { PanelRequestError, problemMessage } from "@/api/problems";

export const API_SCOPES = [
  "read",
  "contacts.read",
  "contacts.write",
  "conversations.read",
  "conversations.write",
  "messages.read",
  "templates.send",
  "directory.read",
] as const;

export type ApiScope = (typeof API_SCOPES)[number];

export const WEBHOOK_EVENTS = [
  "conversation.created",
  "conversation.assigned",
  "conversation.closed",
  "message.created",
] as const;

export type WebhookEvent = (typeof WEBHOOK_EVENTS)[number];

export type ApiToken = {
  id: string;
  scopes: string[];
  request_limit: number;
  revoked: boolean;
  created_at: string;
};

export type IssuedApiToken = ApiToken & {
  secret: string;
};

export type WebhookSubscription = {
  id: string;
  url: string;
  events: string[];
  enabled: boolean;
};

export type IssuedWebhook = WebhookSubscription & {
  signing_secret: string;
};

export type WebhookDelivery = {
  id: string;
  type: string;
  source_id: string;
  status: string;
  attempt: number;
  last_status: number;
  next_attempt_at: string;
  occurred_at: string;
};

export type CreateApiTokenInput = {
  scopes: string[];
  request_limit: number;
};

export type CreateWebhookInput = {
  url: string;
  events: string[];
};

async function panelJSON<T>(method: "GET" | "POST", path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
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
  if (payload === null || typeof payload !== "object") {
    throw new PanelRequestError("Não foi possível concluir. Tente de novo.", response.status);
  }
  return payload as T;
}

export function listApiTokens(signal?: AbortSignal): Promise<ApiToken[]> {
  return panelJSON<{ tokens?: ApiToken[] }>("GET", "/api/panel/api-tokens", undefined, signal).then((body) => body.tokens ?? []);
}

export function createApiToken(input: CreateApiTokenInput): Promise<IssuedApiToken> {
  return panelJSON<IssuedApiToken>("POST", "/api/panel/api-tokens", input);
}

export function revokeApiToken(id: string): Promise<{ id: string; revoked: boolean }> {
  return panelJSON("POST", `/api/panel/api-tokens/${encodeURIComponent(id)}/revoke`);
}

export function listWebhooks(signal?: AbortSignal): Promise<WebhookSubscription[]> {
  return panelJSON<{ subscriptions?: WebhookSubscription[] }>("GET", "/api/panel/outbound-webhooks", undefined, signal).then(
    (body) => body.subscriptions ?? [],
  );
}

export function createWebhook(input: CreateWebhookInput): Promise<IssuedWebhook> {
  return panelJSON<IssuedWebhook>("POST", "/api/panel/outbound-webhooks", input);
}

export function disableWebhook(id: string): Promise<{ id: string; enabled: boolean }> {
  return panelJSON("POST", `/api/panel/outbound-webhooks/${encodeURIComponent(id)}/disable`);
}

export function listWebhookDeliveries(id: string, signal?: AbortSignal): Promise<WebhookDelivery[]> {
  return panelJSON<{ deliveries?: WebhookDelivery[] }>(
    "GET",
    `/api/panel/outbound-webhooks/${encodeURIComponent(id)}/deliveries`,
    undefined,
    signal,
  ).then((body) => body.deliveries ?? []);
}
