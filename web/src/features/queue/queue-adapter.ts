import type { components } from "@/api/schema";
import type { QueueItem } from "./queue-types";
import type { ConversationCard, AttendancePhase } from "@/features/conversation/workspace-types";

type Item = components["schemas"]["RealtimeStoreQueueItem"] & { wait_alert?: boolean };

export function queueItem(item: Item, labels?: ReadonlyMap<string, string>, sectorLabels?: ReadonlyMap<string, string>): QueueItem {
  const statuses: Record<string, string> = { triage: "Em triagem", open: "Aberta", waiting_customer: "Aguardando cliente", closed: "Encerrada" };
  return { avatarLabel: item.contact_label, statusLabel: statuses[item.status] ?? "Estado indisponível", assigneeLabel: item.assignee_id ? (labels?.get(item.assignee_id) ?? "Atendente indisponível") : "Sem responsável", sectorLabel: sectorLabels?.get(item.sector_id), lastMessageAt: item.last_message_at, id: item.id, contactLabel: item.unread && item.unread_count === 0 ? `${item.contact_label} — Não lida` : item.contact_label, protocol: item.protocol, priorityLabel: item.priority === "urgent" ? "Urgente" : null, unreadCount: item.unread_count, waitAlert: item.wait_alert === true };
}

export function queueCard(item: Item, labels?: ReadonlyMap<string, string>): ConversationCard {
  const phases: Record<string, AttendancePhase> = { triage: "triage", open: "open", waiting_customer: "waiting_customer", closed: "closed" };
  return { id: item.id, protocol: item.protocol, contactLabel: item.contact_label, phase: phases[item.status] ?? "triage", assigneeLabel: item.assignee_id ? (labels?.get(item.assignee_id) ?? "Atendente indisponível") : "" };
}
