import type { TemplateCommand } from "@/api/channelpolicy";
import { useSessionStore } from "@/stores/session-store";

export type TemplateIntentScope = {
  tenantId: string;
  userId: string;
  sessionId: string;
  membershipId: string;
  inboxId?: string;
};

const intents = new Map<string, TemplateCommand>();
const listeners = new Set<() => void>();

function emit() {
  for (const listener of listeners) listener();
}

export function templateIntentKey(scope: TemplateIntentScope & { inboxId: string; conversationId: string; templateId: string; language: string }) {
  return JSON.stringify([scope.tenantId, scope.userId, scope.sessionId, scope.membershipId, scope.inboxId, scope.conversationId, scope.templateId, scope.language]);
}

function sameScope(key: string, scope: TemplateIntentScope) {
  const [tenantId, userId, sessionId, membershipId, inboxId] = JSON.parse(key) as string[];
  return tenantId === scope.tenantId && userId === scope.userId && sessionId === scope.sessionId && membershipId === scope.membershipId && (scope.inboxId === undefined || inboxId === scope.inboxId);
}

export function readTemplateIntent(key: string) {
  return intents.get(key) ?? null;
}

export function writeTemplateIntent(key: string, payload: TemplateCommand) {
  intents.set(key, payload);
  emit();
}

export function deleteTemplateIntent(key: string) {
  if (!intents.delete(key)) return;
  emit();
}

/** Tira a entrada somente quando ela ainda é o envio que terminou. */
export function releaseTemplateIntent(key: string, clientRequestId: string): boolean {
  const current = intents.get(key);
  if (current?.client_request_id !== clientRequestId) return false;
  intents.delete(key);
  emit();
  return true;
}

export function revokeTemplateIntents(scope: TemplateIntentScope) {
  let changed = false;
  for (const key of intents.keys()) {
    if (!sameScope(key, scope)) continue;
    intents.delete(key);
    changed = true;
  }
  if (changed) emit();
}

export function clearTemplateIntents() {
  if (intents.size === 0) return;
  intents.clear();
  emit();
}

export function subscribeTemplateIntents(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

useSessionStore.subscribe((next, previous) => {
  const before = previous.principal;
  const after = next.principal;
  if (!before) return;
  if (!after || before.tenantId !== after.tenantId || before.userId !== after.userId || before.sessionId !== after.sessionId || before.membershipId !== after.membershipId) {
    revokeTemplateIntents(before);
  }
});
