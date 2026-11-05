export const MEDIA_KINDS = [
  "image",
  "document",
  "audio",
  "voice",
  "video",
  "sticker",
  "location",
  "contacts",
  "interactive",
  "button",
] as const;

export type MediaKind = (typeof MEDIA_KINDS)[number];

export type InboundContact = {
  name: string;
  phones: string[];
  emails: string[];
  org: string;
  vcard: string;
};

export type MediaAttachmentView = {
  id: string;
  name: string;
  mediaType: string;
  state: string;
  bytes: number;
};

export type MediaContent = {
  mediaKind: MediaKind;
  caption: string;
  filename: string;
  mime: string;
  voice: boolean;
  latitude?: number;
  longitude?: number;
  locationName: string;
  address: string;
  contacts: InboundContact[];
  interactiveType: string;
  replyId: string;
  title: string;
  mediaStatus: string;
  mediaError: string;
  attachment?: MediaAttachmentView;
};

const MEDIA_KIND_LABEL: Record<MediaKind, string> = {
  image: "Imagem",
  document: "Documento",
  audio: "Áudio",
  voice: "Nota de voz",
  video: "Vídeo",
  sticker: "Figurinha",
  location: "Localização",
  contacts: "Contato",
  interactive: "Resposta interativa",
  button: "Botão",
};

const MEDIA_ERROR_LABEL: Record<string, string> = {
  mime_divergente: "O tipo declarado não confere com o conteúdo do arquivo.",
  tamanho_excedido: "O arquivo passa do limite permitido para este tipo.",
  download_falhou: "Não foi possível baixar a mídia do canal.",
  mime_nao_reconhecido: "O tipo do arquivo não foi reconhecido.",
  vazio: "O arquivo está vazio.",
  armazenamento_indisponivel: "O armazenamento está indisponível.",
  limite_excedido: "O limite de armazenamento foi atingido.",
};

export function isMediaKind(value: string): value is MediaKind {
  return (MEDIA_KINDS as readonly string[]).includes(value);
}

export function mediaKindLabel(kind: MediaKind): string {
  return MEDIA_KIND_LABEL[kind];
}

export function mediaErrorLabel(code: string): string {
  if (!code) return "A mídia falhou sem detalhe.";
  return MEDIA_ERROR_LABEL[code] ?? `Falha de mídia (${code}).`;
}

export function mediaStatusLabel(status: string): string | null {
  if (status === "pending" || status === "reserved") return "Download da mídia pendente.";
  if (status === "failed") return "O download da mídia falhou.";
  return null;
}

export function needsServerConversion(mimeType: string): boolean {
  const base = mimeType.toLowerCase().split(";", 1)[0]?.trim() ?? "";
  return (
    base === "audio/webm" ||
    base === "video/webm" ||
    base === "audio/ogg" ||
    base === "video/ogg" ||
    base === "application/ogg" ||
    base === "audio/mpeg" ||
    base === "audio/mp3" ||
    base === "audio/mp4" ||
    base === "audio/m4a" ||
    base === "audio/x-m4a" ||
    base === "audio/wav" ||
    base === "audio/wave" ||
    base === "audio/x-wav"
  );
}

export function audioFileName(mimeType: string): string {
  const base = mimeType.toLowerCase().split(";", 1)[0]?.trim() ?? "";
  if (base === "audio/ogg" || base === "video/ogg" || base === "application/ogg") return "nota-de-voz.ogg";
  if (base === "audio/webm" || base === "video/webm") return "nota-de-voz.webm";
  if (base === "audio/mpeg") return "nota-de-voz.mp3";
  if (base === "audio/mp4") return "nota-de-voz.m4a";
  return "nota-de-voz.bin";
}

export function messagePreview(message: { body?: string; content?: { [key: string]: unknown } }): string {
  const content = message.content ?? {};
  const candidates = [content.caption, content.title, content.filename, content.location_name, content.address, message.body];
  for (const candidate of candidates) {
    if (typeof candidate === "string" && candidate.trim()) return candidate.trim();
  }
  return "Mensagem sem texto";
}

export type ReactionView = { emoji: string; actor: string; externalId: string };

export function readReactions(
  reactions: readonly { emoji?: string; actor?: string; external_id?: string }[] | null | undefined,
): ReactionView[] {
  if (!reactions) return [];
  const visible: ReactionView[] = [];
  for (const reaction of reactions) {
    const emoji = reaction.emoji?.trim() ?? "";
    if (!emoji) continue;
    visible.push({ emoji, actor: reaction.actor ?? "", externalId: reaction.external_id ?? "" });
  }
  return visible;
}

export function readMediaContent(
  kind: MediaKind,
  content: { [key: string]: unknown } | undefined,
  attachment: { id: string; name: string; media_type: string; state: string; bytes: number } | undefined,
  body: string,
): MediaContent {
  const source = content ?? {};
  const caption = text(source.caption) || body;
  const filename = text(source.filename);
  const mime = text(source.mime);
  const mediaStatus = text(source.media_status);
  const mediaError = text(source.media_error);
  const latitude = finite(source.latitude);
  const longitude = finite(source.longitude);
  let view = attachment
    ? {
        id: attachment.id,
        name: attachment.name,
        mediaType: attachment.media_type,
        state: attachment.state,
        bytes: attachment.bytes,
      }
    : undefined;
  const attachmentId = text(source.attachment_id);
  if (!view && attachmentId && mediaStatus !== "failed" && !mediaError) {
    view = {
      id: attachmentId,
      name: filename || "arquivo",
      mediaType: mime,
      state: mediaStatus === "stored" ? "validated" : mediaStatus,
      bytes: finite(source.bytes) ?? 0,
    };
  }
  return {
    mediaKind: kind,
    caption,
    filename,
    mime,
    voice: source.voice === true || kind === "voice",
    latitude,
    longitude,
    locationName: text(source.location_name),
    address: text(source.address),
    contacts: readContacts(source.contacts),
    interactiveType: text(source.interactive_type),
    replyId: text(source.reply_id),
    title: text(source.title),
    mediaStatus,
    mediaError,
    attachment: view,
  };
}

export function mapHref(latitude: number | undefined, longitude: number | undefined): string | null {
  if (latitude === undefined || longitude === undefined) return null;
  if (latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180) return null;
  return `https://www.openstreetmap.org/?mlat=${latitude}&mlon=${longitude}#map=16/${latitude}/${longitude}`;
}

export function entryPreview(entry: {
  kind: string;
  text?: string;
  summary?: string;
  reference?: string;
  caption?: string;
  title?: string;
  filename?: string;
  locationName?: string;
  address?: string;
  mediaKind?: MediaKind;
}): string {
  if (entry.kind === "text") return entry.text?.trim() || "Mensagem sem texto";
  if (entry.kind === "event") return entry.summary?.trim() || "Evento";
  if (entry.kind === "unsupported") return entry.reference?.trim() || "Conteúdo não suportado";
  return (
    entry.caption?.trim() ||
    entry.title?.trim() ||
    entry.filename?.trim() ||
    entry.locationName?.trim() ||
    entry.address?.trim() ||
    (entry.mediaKind ? mediaKindLabel(entry.mediaKind) : "Mídia")
  );
}

const NIL_UUID = "00000000-0000-0000-0000-000000000000";

export function quoteTarget(
  replyToMessageId: string | undefined,
  content: { [key: string]: unknown } | undefined,
  messages: readonly { id: string; body?: string; content?: { [key: string]: unknown } }[],
): { id: string; preview: string } | undefined {
  const fromContent = content?.reply_to_message_id;
  const id = replyToMessageId || (typeof fromContent === "string" ? fromContent : "");
  // The API serializes "no quote" as the zero UUID; it is not a message.
  if (!id || id === NIL_UUID) return undefined;
  const target = messages.find((item) => item.id === id);
  return { id, preview: target ? messagePreview(target) : "Mensagem citada indisponível" };
}

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function finite(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function readContacts(value: unknown): InboundContact[] {
  if (!Array.isArray(value)) return [];
  const contacts: InboundContact[] = [];
  for (const item of value) {
    if (!item || typeof item !== "object" || Array.isArray(item)) continue;
    const record = item as { [key: string]: unknown };
    contacts.push({
      name: text(record.name),
      phones: stringList(record.phones),
      emails: stringList(record.emails),
      org: text(record.org),
      vcard: text(record.vcard),
    });
  }
  return contacts;
}

function stringList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string");
}
