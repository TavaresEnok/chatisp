import type { QueryClient } from "@tanstack/react-query";
import type { PanelPrincipal } from "@/stores/session-store";

export function attachmentKey(principal: PanelPrincipal, ...rest: readonly string[]) {
  return ["m4a-attachment", principal.tenantId, principal.userId, principal.sessionId, principal.membershipId, ...rest] as const;
}

export function forgetAttachmentQueries(client: QueryClient, principal: PanelPrincipal, inboxId?: string, conversationId?: string) {
  const prefix = attachmentKey(principal);
  const matches = (queryKey: readonly unknown[]) =>
    prefix.every((part, index) => queryKey[index] === part) &&
    (inboxId === undefined || queryKey.includes(inboxId)) &&
    (conversationId === undefined || queryKey.includes(conversationId));
  void client.cancelQueries({ predicate: (query) => matches(query.queryKey) });
  client.removeQueries({ predicate: (query) => matches(query.queryKey) });
}
