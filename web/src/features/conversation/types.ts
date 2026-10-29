/** View models de apresentação. Não são DTOs nem o contrato HTTP. */

export type ConversationScope = {
  tenantId: string;
  userId: string;
  conversationId: string;
};

/** Fila local do envio. Distinto de entrega e leitura. */
export type LocalSendState =
  | "queued"
  | "sending"
  | "accepted"
  | "failed"
  | "retrying"
  | "uncertain"
  | "cancelled";

/** Recibo remoto. Não substitui o estado local. */
export type DeliveryReceipt = "delivered" | "read";

export type TextTimelineEntry = {
  kind: "text";
  id: string;
  author: string;
  text: string;
  occurredAt?: string;
  localState?: LocalSendState;
  receipt?: DeliveryReceipt;
};

export type EventTimelineEntry = {
  kind: "event";
  id: string;
  author: string;
  summary: string;
  occurredAt?: string;
};

export type UnsupportedTimelineEntry = {
  kind: "unsupported";
  id: string;
  author: string;
  reference: string;
  occurredAt?: string;
};

export type TimelineEntry =
  | TextTimelineEntry
  | EventTimelineEntry
  | UnsupportedTimelineEntry;

export type TimelineViewStatus = "loading" | "error" | "ready";

export type SendTextCommand = {
  intentKey: string;
  text: string;
};

export type SendTextResult =
  | { outcome: "accepted" }
  | { outcome: "failed"; detail: string }
  | { outcome: "uncertain"; detail: string }
  | { outcome: "lost" };
