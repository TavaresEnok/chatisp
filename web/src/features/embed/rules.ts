import type { EmbedContext, EmbedView } from "@/api/embed";

const allowedKeys = ["type", "v", "conversation_id", "protocol", "sector_id", "inbox_id"] as const;

export function pageOrigin(raw: string): string | null {
  try {
    const url = new URL(raw);
    if (url.username || url.password || url.search || url.hash) return null;
    if (url.protocol !== "https:" && url.protocol !== "http:") return null;
    return url.origin;
  } catch {
    return null;
  }
}

export function contextMessage(input: {
  conversation_id?: string;
  protocol?: string;
  sector_id?: string;
  inbox_id?: string;
  body?: string;
  cpf?: string;
  secret?: string;
  message?: string;
}): EmbedContext {
  return {
    type: "chatisp.embed.context",
    v: 1,
    conversation_id: input.conversation_id ?? "",
    protocol: input.protocol ?? "",
    sector_id: input.sector_id ?? "",
    inbox_id: input.inbox_id ?? "",
  };
}

export function canEmbed(view: Partial<EmbedView> | null | undefined): boolean {
  if (!view || view.embed !== true || !view.url || !view.origin || !view.context) return false;
  if (pageOrigin(view.url) !== view.origin || pageOrigin(view.origin) !== view.origin) return false;
  const message = contextMessage(view.context);
  if (!message.conversation_id || !message.sector_id || !message.inbox_id) return false;
  if (view.context.type !== "chatisp.embed.context" || view.context.v !== 1) return false;
  const leaked = JSON.stringify(message);
  return !leaked.includes("body") && allowedKeys.every((key) => key in message);
}

export function publish(
  target: { postMessage: (data: EmbedContext, origin: string) => void } | null,
  context: EmbedContext,
  origin: string,
) {
  if (!target || pageOrigin(origin) !== origin) return;
  target.postMessage(contextMessage(context), origin);
}

export function acceptFrameMessage(event: { origin: string; source: unknown }, origin: string, frame: unknown): boolean {
  return frame != null && origin !== "" && event.origin === origin && event.source === frame;
}
