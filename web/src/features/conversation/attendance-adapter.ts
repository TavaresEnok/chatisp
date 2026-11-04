import type { Schema } from "@/api/attendance";
import type { ConversationCard, AttendancePhase } from "@/features/conversation/workspace-types";
import type { TimelineEntry, LocalSendState } from "@/features/conversation/types";

export function card(head: Schema["Head"]): ConversationCard {
  const phases: Record<string, AttendancePhase> = { triage: "triage", open: "open", waiting_customer: "waiting_customer", closed: "closed" };
  return { id: head.id, protocol: head.protocol, contactLabel: head.contact_id, assigneeLabel: head.assignee_id, phase: phases[head.status] ?? "triage" };
}
export function timeline(history: Schema["HistoryOutputBody"]): TimelineEntry[] {
  const messages: TimelineEntry[] = (history.messages ?? []).map((m) => ({ kind: "text", id: m.id, author: m.direction === "inbound" ? "Cliente" : "Atendimento", text: m.body, occurredAt: m.trusted_at || m.ingested_at, ...(["queued", "sending", "accepted", "failed", "retrying", "uncertain", "cancelled"].includes(m.local_state) ? { localState: m.local_state as LocalSendState } : {}) }));
  const events: TimelineEntry[] = (history.timeline ?? []).map((e) => ({ kind: "event", id: e.id, author: e.actor_id ? "Equipe" : "Sistema", summary: eventSummary(e.kind), occurredAt: e.at }));
  return [...messages, ...events].sort ( (a,b) => (a.occurredAt ?? "").localeCompare(b.occurredAt ?? ""));
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
