import type { Schema } from "@/api/attendance";
import type { ConversationCard, AttendancePhase } from "@/features/conversation/workspace-types";
import type { LocalSendState, TimelineEntry } from "@/features/conversation/types";
import { isMediaKind, quoteTarget, readMediaContent, readReactions } from "@/features/media/model";

const LOCAL_STATES = new Set(["queued", "sending", "accepted", "failed", "retrying", "uncertain", "cancelled"]);

export function card(head: Schema["Head"]): ConversationCard {
  const phases: Record<string, AttendancePhase> = { triage: "triage", open: "open", waiting_customer: "waiting_customer", closed: "closed" };
  return { id: head.id, protocol: head.protocol, contactLabel: head.contact_id, assigneeLabel: head.assignee_id, phase: phases[head.status] ?? "triage" };
}

export function timeline(history: Schema["HistoryOutputBody"]): TimelineEntry[] {
  const source = history.messages ?? [];
  const messages: TimelineEntry[] = source.map((message) => messageEntry(message, source));
  const events: TimelineEntry[] = (history.timeline ?? []).map((event) => ({
    kind: "event",
    id: event.id,
    author: event.actor_id ? "Equipe" : "Sistema",
    summary: eventSummary(event.kind),
    occurredAt: event.at,
  }));
  return [...messages, ...events].sort((left, right) => (left.occurredAt ?? "").localeCompare(right.occurredAt ?? ""));
}

function messageEntry(message: Schema["Message"], messages: readonly Schema["Message"][]): TimelineEntry {
  const state = LOCAL_STATES.has(message.local_state) ? (message.local_state as LocalSendState) : undefined;
  const reactions = readReactions(message.reactions);
  const replyTo = quoteTarget(message.reply_to_message_id, message.content, messages);
  const shared = {
    id: message.id,
    author: message.direction === "inbound" ? "Cliente" : "Atendimento",
    occurredAt: message.trusted_at || message.ingested_at,
    ...(state ? { localState: state } : {}),
    ...(reactions.length > 0 ? { reactions } : {}),
    ...(replyTo ? { replyTo } : {}),
  };
  const kind = message.content_kind ?? "text";
  if (!isMediaKind(kind)) {
    return { kind: "text", text: message.body, ...shared };
  }
  return { kind: "media", ...shared, ...readMediaContent(kind, message.content, message.attachment, message.body) };
}

const plainEvents: Record<string, string> = {
  assumed: "Atendimento assumido",
  controller_changed: "Controle do atendimento alterado",
  closed: "Atendimento encerrado",
  reopened: "Atendimento reaberto (novo ciclo, mesmo protocolo)",
  wait_started: "Aguardando resposta do cliente",
  wait_ended: "Cliente respondeu",
  customer_message: "Mensagem do cliente",
  outbound_queued: "Resposta enviada para a fila de envio",
  snoozed: "Conversa adiada",
  snooze_cleared: "Adiamento encerrado",
};
const assignmentEvents: Record<string, string> = {
  transferred_member: "Transferida para outro atendente",
  transferred_sector: "Transferida para outro setor",
  unassigned: "Devolvida à fila",
  distributed: "Atribuída automaticamente",
  snooze_return: "Retorno do adiamento",
  offline_unassigned: "Devolvida à fila: atendente offline além da tolerância",
};
/** Human label for a timeline kind. Assignment kinds carry ids after ":"; they are not shown. */
export function eventSummary(kind: string): string {
  if (plainEvents[kind]) return plainEvents[kind];
  const prefix = kind.split(":", 1)[0] ?? kind;
  return assignmentEvents[prefix] ?? "Evento do atendimento";
}
