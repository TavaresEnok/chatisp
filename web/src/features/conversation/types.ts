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

export type ReplyTarget = {
  id: string;
  preview: string;
};

export type MessageReactionView = {
  emoji: string;
  actor: string;
  externalId: string;
};

export type TextTimelineEntry = {
  direction?: "inbound" | "outbound";
  kind: "text";
  id: string;
  author: string;
  text: string;
  occurredAt?: string;
  localState?: LocalSendState;
  receipt?: DeliveryReceipt;
  reactions?: readonly MessageReactionView[];
  replyTo?: ReplyTarget;
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

export type MediaTimelineEntry = {
  direction?: "inbound" | "outbound";
  kind: "media";
  id: string;
  author: string;
  occurredAt?: string;
  localState?: LocalSendState;
  receipt?: DeliveryReceipt;
  mediaKind:
    | "image"
    | "document"
    | "audio"
    | "voice"
    | "video"
    | "sticker"
    | "location"
    | "contacts"
    | "interactive"
    | "button";
  caption: string;
  filename: string;
  mime: string;
  voice: boolean;
  latitude?: number;
  longitude?: number;
  locationName: string;
  address: string;
  contacts: { name: string; phones: string[]; emails: string[]; org: string; vcard: string }[];
  interactiveType: string;
  replyId: string;
  title: string;
  mediaStatus: string;
  mediaError: string;
  attachment?: { id: string; name: string; mediaType: string; state: string; bytes: number };
  reactions?: readonly MessageReactionView[];
  replyTo?: ReplyTarget;
};

export type TimelineEntry =
  | TextTimelineEntry
  | EventTimelineEntry
  | UnsupportedTimelineEntry
  | MediaTimelineEntry;

export type TimelineViewStatus = "loading" | "error" | "ready";

export type SendTextCommand = {
  intentKey: string;
  text: string;
  replyToMessageId?: string;
};

export type SendMediaCommand = {
  intentKey: string;
  caption: string;
  file: File;
  replyToMessageId?: string;
};

export type SendTextResult =
  | { outcome: "accepted" }
  | { outcome: "failed"; detail: string; conflict?: boolean }
  | { outcome: "uncertain"; detail: string }
  | { outcome: "lost" };

export type SendAudioCommand = {
  intentKey: string;
  blob: Blob;
  mimeType: string;
  replyToMessageId?: string;
};
