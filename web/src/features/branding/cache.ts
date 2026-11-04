import type { QueryClient } from "@tanstack/react-query";

export function brandingQueryKey(tenantId: string, sessionId: string) {
  return ["branding", tenantId, sessionId] as const;
}

export function invalidateBranding(client: QueryClient) {
  return client.invalidateQueries({ queryKey: ["branding"] });
}
