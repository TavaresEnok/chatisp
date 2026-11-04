import type { ComposerState, PendingIntent } from "@/features/conversation/send-intent";
import type { SendTextResult } from "@/features/conversation/types";
import type { ConversationScope } from "@/features/conversation/types";
import { useSessionStore } from "@/stores/session-store";

const prefix = "chatisp:draft:";
type Settlement = { intentKey: string; result: SendTextResult };
const listeners = new Map<string, Set<(settlement: Settlement) => void>>();

export function subscribeDraftSettlement(key: string, listener: (settlement: Settlement) => void) {
  const subscribers = listeners.get(key) ?? new Set();
  subscribers.add(listener);
  listeners.set(key, subscribers);
  return () => {
    subscribers.delete(listener);
    if (!subscribers.size) listeners.delete(key);
  };
}

export function notifyDraftSettlement(key: string, settlement: Settlement) {
  listeners.get(key)?.forEach((listener) => listener(settlement));
}
export type DraftIdentity = { tenantId: string; userId: string; sessionId: string };

export function draftKey(identity: DraftIdentity, scope: ConversationScope): string {
  return prefix + JSON.stringify([identity.tenantId, identity.userId, identity.sessionId, scope.conversationId]);
}

function readPending(value: ComposerState["pending"]): PendingIntent | null {
  if (!value || typeof value.intentKey !== "string" || typeof value.text !== "string") return null;
  if (typeof value.replyToMessageId === "string" && value.replyToMessageId.length > 0) {
    return { intentKey: value.intentKey, text: value.text, replyToMessageId: value.replyToMessageId };
  }
  return { intentKey: value.intentKey, text: value.text };
}

export function readDraft(key: string): ComposerState | undefined {
  try {
    const value = JSON.parse(localStorage.getItem(key) ?? "null") as ComposerState | null;
    if (!value || typeof value.draft !== "string") return undefined;
    const pending = readPending(value.pending);
    return { draft: value.draft, pending, phase: pending ? "uncertain" : "idle", notice: pending ? "A tentativa anterior não foi confirmada. A mesma intenção será repetida." : null };
  } catch { return undefined; }
}

export function writeDraft(key: string, state: ComposerState) {
  try {
    if (!state.draft && !state.pending) localStorage.removeItem(key);
    else localStorage.setItem(key, JSON.stringify({ draft: state.draft, pending: state.pending }));
  } catch { /* armazenamento pode estar indisponível */ }
}

export function clearDrafts() {
  try {
    for (let i = localStorage.length - 1; i >= 0; i--) {
      const key = localStorage.key(i);
      if (key?.startsWith(prefix)) localStorage.removeItem(key);
    }
  } catch { /* armazenamento pode estar indisponível */ }
}

// A assinatura é síncrona: limpa antes que uma sessão nova possa ler os dados.
useSessionStore.subscribe((next, previous) => {
  const before = previous.principal;
  const after = next.principal;
  if (before && (!after || before.tenantId !== after.tenantId || before.userId !== after.userId)) clearDrafts();
});
