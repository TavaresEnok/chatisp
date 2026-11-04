import type { WebhookDelivery } from "@/api/integrations";
import { API_SCOPES, WEBHOOK_EVENTS } from "@/api/integrations";
import { PanelRequestError, errorMessage } from "@/api/problems";
import type { PanelPrincipal } from "@/stores/session-store";

export const SCOPE_LABELS: Record<string, string> = {
  read: "Leitura geral",
  "contacts.read": "Leitura de contatos",
  "contacts.write": "Escrita de contatos",
  "conversations.read": "Leitura de conversas",
  "messages.read": "Leitura de mensagens",
  "templates.send": "Envio de template",
  "directory.read": "Leitura de cadastros",
};

export const EVENT_LABELS: Record<string, string> = {
  "conversation.created": "Conversa criada",
  "conversation.assigned": "Conversa atribuída",
  "conversation.closed": "Conversa encerrada",
  "message.created": "Mensagem criada",
};

const DELIVERY_LABELS: Record<string, string> = {
  pending: "pendente",
  delivered: "entregue",
  dead: "encerrada",
};

export function canManageIntegrations(principal: PanelPrincipal | null): principal is PanelPrincipal {
  return Boolean(principal && principal.role === "admin" && !principal.mfaEnrollmentRequired);
}

export function scopeLabel(scope: string): string {
  return SCOPE_LABELS[scope] ?? scope;
}

export function eventLabel(event: string): string {
  return EVENT_LABELS[event] ?? event;
}

export function deliveryStatusLabel(status: string): string {
  return DELIVERY_LABELS[status] ?? status;
}

export function orderedScopes(scopes: readonly string[]): string[] {
  const rank = new Map<string, number>(API_SCOPES.map((scope, index) => [scope, index]));
  return [...scopes].sort((left, right) => (rank.get(left) ?? 99) - (rank.get(right) ?? 99));
}

export function orderedEvents(events: readonly string[]): string[] {
  const rank = new Map<string, number>(WEBHOOK_EVENTS.map((event, index) => [event, index]));
  return [...events].sort((left, right) => (rank.get(left) ?? 99) - (rank.get(right) ?? 99));
}

export function latestDelivery(rows: readonly WebhookDelivery[]): WebhookDelivery | null {
  let best: WebhookDelivery | null = null;
  let bestTime = Number.NEGATIVE_INFINITY;
  for (const row of rows) {
    const time = Date.parse(row.occurred_at);
    if (!Number.isFinite(time)) continue;
    if (best === null || time >= bestTime) {
      best = row;
      bestTime = time;
    }
  }
  return best;
}

export function webhookSubmitMessage(error: unknown): string {
  if (error instanceof PanelRequestError && error.status === 422 && error.message === "destino ou evento inválido") {
    return "URL recusada. Destino ou evento inválido.";
  }
  return errorMessage(error);
}

export function parseRequestLimit(raw: string): number | null {
  const parsed = Number(raw);
  if (!Number.isInteger(parsed) || parsed < 1 || parsed > 6000) return null;
  return parsed;
}
