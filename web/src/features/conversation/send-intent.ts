import type { ConversationScope, SendTextResult } from "@/features/conversation/types";

export const SUBMITTING_NOTICE = "Enviando. O conector ainda não confirmou.";
export const OFFLINE_NOTICE =
  "Sem conexão com o servidor. O rascunho permanece e o envio não foi confirmado.";
export const ACCEPTED_NOTICE = "Mensagem aceita pelo conector.";
export const FAILED_FALLBACK = "O envio falhou. O rascunho permanece.";
export const UNCERTAIN_FALLBACK = "Resultado incerto. O rascunho permanece.";
export const LOST_NOTICE =
  "A resposta do envio não chegou. O rascunho permanece e a mesma tentativa será repetida.";

export type ComposerPhase =
  | "idle"
  | "submitting"
  | "offline"
  | "accepted"
  | "failed"
  | "uncertain";

export type PendingIntent = {
  intentKey: string;
  text: string;
  replyToMessageId?: string;
};

export type ComposerState = {
  draft: string;
  phase: ComposerPhase;
  notice: string | null;
  pending: PendingIntent | null;
};

export type IntentSubmitMode = "reuse-if-pending" | "new-decision";

export type ComposerAction =
  | { type: "edit"; draft: string }
  | { type: "submit"; offline: boolean; intentKey: string; text: string; replyToMessageId?: string }
  | { type: "result"; intentKey: string; result: SendTextResult };

export function createComposerState(): ComposerState {
  return { draft: "", phase: "idle", notice: null, pending: null };
}

export function conversationScopeKey(scope: ConversationScope): string {
  return `${scope.tenantId}\u001f${scope.userId}\u001f${scope.conversationId}`;
}

export function createIntentKey(): string {
  return globalThis.crypto.randomUUID();
}

/**
 * Reutiliza a chave da intenção enquanto o resultado não chegou ou ficou incerto:
 * o conector pode ter aceitado, e outra chave duplicaria o envio.
 * Falha conhecida, sucesso ou a decisão de enviar como nova mensagem geram outra chave.
 */
export function selectClientRequestId(
  state: ComposerState,
  mode: IntentSubmitMode,
  createKey: () => string,
): string | null {
  if (state.phase === "submitting") return null;
  if (state.draft.trim() === "") return null;
  if (
    mode === "reuse-if-pending" &&
    state.pending !== null &&
    state.pending.text === state.draft
  ) {
    return state.pending.intentKey;
  }
  return createKey();
}

function detailOr(detail: string, fallback: string): string {
  const trimmed = detail.trim();
  return trimmed.length > 0 ? trimmed : fallback;
}

export function reduceComposer(
  state: ComposerState,
  action: ComposerAction,
): ComposerState {
  switch (action.type) {
    case "edit":
      if (action.draft === state.draft) return state;
      return {
        ...state,
        draft: action.draft,
        phase: state.phase === "accepted" ? "idle" : state.phase,
        notice: state.phase === "accepted" ? null : state.notice,
      };
    case "submit": {
      if (state.phase === "submitting") return state;
      if (action.text.trim() === "") return state;
      const pending: PendingIntent = {
        intentKey: action.intentKey,
        text: action.text,
        ...(action.replyToMessageId ? { replyToMessageId: action.replyToMessageId } : {}),
      };
      if (action.offline) {
        return {
          draft: action.text,
          phase: "offline",
          notice: OFFLINE_NOTICE,
          pending,
        };
      }
      return {
        draft: action.text,
        phase: "submitting",
        notice: SUBMITTING_NOTICE,
        pending,
      };
    }
    case "result": {
      if (!state.pending || state.pending.intentKey !== action.intentKey) {
        return state;
      }
      if (action.result.outcome === "accepted") {
        return {
          draft: state.draft === state.pending.text ? "" : state.draft,
          phase: "accepted",
          notice: ACCEPTED_NOTICE,
          pending: null,
        };
      }
      if (action.result.outcome === "failed") {
        return {
          ...state,
          phase: "failed",
          notice: detailOr(action.result.detail, FAILED_FALLBACK),
          pending: null,
        };
      }
      if (action.result.outcome === "uncertain") {
        return {
          ...state,
          phase: "uncertain",
          notice: detailOr(action.result.detail, UNCERTAIN_FALLBACK),
          pending: state.pending,
        };
      }
      return {
        ...state,
        phase: "uncertain",
        notice: LOST_NOTICE,
        pending: state.pending,
      };
    }
    default:
      return state;
  }
}
