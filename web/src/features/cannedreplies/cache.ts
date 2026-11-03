import type { QueryClient } from "@tanstack/react-query";
import type { PanelPrincipal } from "@/stores/session-store";

export function cannedKey(principal: PanelPrincipal, ...rest: readonly string[]) {
  return ["m4a-canned", principal.tenantId, principal.userId, principal.sessionId, principal.membershipId, ...rest] as const;
}

export function forgetCannedQueries(client: QueryClient, principal: PanelPrincipal) {
  const prefix = cannedKey(principal);
  const matches = (queryKey: readonly unknown[]) => prefix.every((part, index) => queryKey[index] === part);
  void client.cancelQueries({ predicate: (query) => matches(query.queryKey) });
  client.removeQueries({ predicate: (query) => matches(query.queryKey) });
}

export function cannedScopeToken(principal: PanelPrincipal) {
  return [principal.tenantId, principal.userId, principal.sessionId, principal.membershipId].join("\0");
}

type DenialListener = (token: string, message: string) => void;

const denialListeners = new Set<DenialListener>();

export function subscribeCannedDenial(listener: DenialListener) {
  denialListeners.add(listener);
  return () => {
    denialListeners.delete(listener);
  };
}

/** 403/404 de leitura ou mutação: avisa quem está montado e apaga a faixa. Não gruda na próxima instância. */
export function denyCannedScope(client: QueryClient, principal: PanelPrincipal, message: string) {
  const token = cannedScopeToken(principal);
  for (const listener of denialListeners) listener(token, message);
  forgetCannedQueries(client, principal);
}
