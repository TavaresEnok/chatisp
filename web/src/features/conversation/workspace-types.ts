/** View models de apresentação do atendimento. Não são DTOs nem o contrato HTTP. */

import type { TimelineEntry, TimelineViewStatus } from "@/features/conversation/types";

export type AttendanceScope = {
  tenantId: string;
  userId: string;
};

/** Fase visível na fila. Os nomes seguem o produto, não um payload de API. */
export type AttendancePhase = "triage" | "open" | "waiting_customer" | "closed";

export type CloseResult = "resolved" | "no_response" | "referred_external" | "spam";

export type ConversationListStatus = "loading" | "error" | "ready";

export type ConversationCard = {
  id: string;
  protocol: string;
  contactLabel: string;
  phase: AttendancePhase;
  assigneeLabel: string;
};

export type CloseAttendanceFields = {
  reason: string;
  result: CloseResult;
  synthesis: string;
};

export type CloseAttendanceCommand = CloseAttendanceFields & {
  conversationId: string;
};

export type AttendanceActionResult =
  | { outcome: "done" }
  | { outcome: "conflict"; detail: string }
  | { outcome: "failed"; detail: string };

export type ConversationDetailBinding = {
  conversationId: string | null;
  status: TimelineViewStatus;
  entries: readonly TimelineEntry[];
};

export const ATTENDANCE_PHASE_LABEL: Record<AttendancePhase, string> = {
  triage: "Em triagem",
  open: "Aberta",
  waiting_customer: "Aguardando cliente",
  closed: "Encerrada",
};

export const CLOSE_RESULT_LABEL: Record<CloseResult, string> = {
  resolved: "Resolvido",
  no_response: "Sem resposta",
  referred_external: "Encaminhado",
  spam: "Spam",
};

export const CLOSE_RESULTS = [
  "resolved",
  "no_response",
  "referred_external",
  "spam",
] as const satisfies readonly CloseResult[];

const CONFLICT_FALLBACK = "A conversa mudou. Atualize antes de repetir.";
const FAILED_FALLBACK = "Não foi possível concluir a ação.";
const LOST_FALLBACK = "A resposta não chegou.";

export function presentationText(value: string, fallback: string): string {
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : fallback;
}

export function readAttendanceResult(value: unknown): AttendanceActionResult {
  if (!value || typeof value !== "object" || !("outcome" in value)) {
    return { outcome: "failed", detail: LOST_FALLBACK };
  }
  const outcome = value.outcome;
  if (outcome === "done") return { outcome: "done" };
  if (outcome === "conflict" || outcome === "failed") {
    const detail = "detail" in value && typeof value.detail === "string" ? value.detail.trim() : "";
    return {
      outcome,
      detail: detail || (outcome === "conflict" ? CONFLICT_FALLBACK : FAILED_FALLBACK),
    };
  }
  return { outcome: "failed", detail: LOST_FALLBACK };
}
