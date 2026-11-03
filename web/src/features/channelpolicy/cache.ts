import type { QueryClient } from "@tanstack/react-query";
import { revokeTemplateIntents } from "@/features/channelpolicy/template-intent";
import type { PanelPrincipal } from "@/stores/session-store";

export function policyKey(principal: PanelPrincipal, ...rest: readonly string[]) {
  return ["m4a-policy", principal.tenantId, principal.userId, principal.sessionId, principal.membershipId, ...rest] as const;
}

export function policyScopeToken(principal: PanelPrincipal, inboxId: string) {
  return [principal.tenantId, principal.userId, principal.sessionId, principal.membershipId, inboxId].join("\0");
}

export function forgetPolicyQueries(client: QueryClient, principal: PanelPrincipal, inboxId?: string) {
  const prefix = policyKey(principal);
  const matches = (queryKey: readonly unknown[]) =>
    prefix.every((part, index) => queryKey[index] === part) && (inboxId === undefined || queryKey.includes(inboxId));
  void client.cancelQueries({ predicate: (query) => matches(query.queryKey) });
  client.removeQueries({ predicate: (query) => matches(query.queryKey) });
}

type DenialListener = (token: string, message: string) => void;

const denialListeners = new Set<DenialListener>();

export function subscribePolicyDenial(listener: DenialListener) {
  denialListeners.add(listener);
  return () => {
    denialListeners.delete(listener);
  };
}

/** 403/404 de leitura ou mutação: avisa quem está montado, cancela a faixa e apaga intenções. Não gruda na próxima instância. */
export function denyPolicyScope(client: QueryClient, principal: PanelPrincipal, inboxId: string, message: string) {
  const token = policyScopeToken(principal, inboxId);
  for (const listener of denialListeners) listener(token, message);
  forgetPolicyQueries(client, principal, inboxId);
  revokeTemplateIntents({ ...principal, inboxId });
}
