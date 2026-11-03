import type { QueryClient } from "@tanstack/react-query";
import type { PanelPrincipal } from "@/stores/session-store";

export function hoursKey(principal: PanelPrincipal, ...rest: readonly string[]) {
  return ["m4a-hours", principal.tenantId, principal.userId, principal.sessionId, principal.membershipId, ...rest] as const;
}

export function forgetHoursQueries(client: QueryClient, principal: PanelPrincipal, inboxId?: string) {
  const prefix = hoursKey(principal);
  const matches = (queryKey: readonly unknown[]) =>
    prefix.every((part, index) => queryKey[index] === part) && (inboxId === undefined || queryKey.includes(inboxId));
  void client.cancelQueries({ predicate: (query) => matches(query.queryKey) });
  client.removeQueries({ predicate: (query) => matches(query.queryKey) });
}

export function hoursScopeToken(principal: PanelPrincipal) {
  return [principal.tenantId, principal.userId, principal.sessionId, principal.membershipId].join("\0");
}

type DenialListener = (token: string, message: string) => void;

const denialListeners = new Set<DenialListener>();

export function subscribeHoursDenial(listener: DenialListener) {
  denialListeners.add(listener);
  return () => {
    denialListeners.delete(listener);
  };
}

/** 403/404: avisa a seção montada e apaga a faixa deste vínculo. Não gruda na próxima instância. */
export function denyHoursScope(client: QueryClient, principal: PanelPrincipal, message: string) {
  const token = hoursScopeToken(principal);
  for (const listener of denialListeners) listener(token, message);
  forgetHoursQueries(client, principal);
}
