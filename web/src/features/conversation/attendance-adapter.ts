import type { Schema } from "@/api/attendance";
import type { ConversationCard, AttendancePhase } from "@/features/conversation/workspace-types";
import type { TimelineEntry, LocalSendState } from "@/features/conversation/types";

export function card(head: Schema["Head"]): ConversationCard {
  const phases: Record<string, AttendancePhase> = { triage: "triage", open: "open", waiting_customer: "waiting_customer", closed: "closed" };
  return { id: head.id, protocol: head.protocol, contactLabel: head.contact_id, assigneeLabel: head.assignee_id, phase: phases[head.status] ?? "triage" };
}
export function timeline(history: Schema["HistoryOutputBody"]): TimelineEntry[] {
  const messages: TimelineEntry[] = (history.messages ?? []).map((m) => ({ kind: "text", id: m.id, author: m.direction === "inbound" ? "Cliente" : "Atendimento", text: m.body, occurredAt: m.trusted_at || m.ingested_at, ...(["queued", "sending", "accepted", "failed", "retrying", "uncertain", "cancelled"].includes(m.local_state) ? { localState: m.local_state as LocalSendState } : {}) }));
  const events: TimelineEntry[] = (history.timeline ?? []).map((e) => ({ kind: "event", id: e.id, author: e.actor_id || "Sistema", summary: e.kind, occurredAt: e.at }));
  return [...messages, ...events].sort ( (a,b) => (a.occurredAt ?? "").localeCompare(b.occurredAt ?? ""));
}
