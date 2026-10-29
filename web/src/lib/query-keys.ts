import type { AreaId } from "@/lib/areas";

export const queryKeys = {
  tenant: (tenantId: string) => [tenantId] as const,
  dashboard: (tenantId: string) => [tenantId, "dashboard"] as const,
  conversations: (tenantId: string) => [tenantId, "conversations"] as const,
  conversation: (tenantId: string, conversationId: string) =>
    [tenantId, "conversations", conversationId] as const,
  contacts: (tenantId: string) => [tenantId, "contacts"] as const,
  contact: (tenantId: string, contactId: string) =>
    [tenantId, "contacts", contactId] as const,
  teams: (tenantId: string) => [tenantId, "teams"] as const,
  reports: (tenantId: string) => [tenantId, "reports"] as const,
  settings: (tenantId: string) => [tenantId, "settings"] as const,
  memberships: (tenantId: string) => [tenantId, "settings", "memberships"] as const,
  sessions: (tenantId: string) => [tenantId, "settings", "sessions"] as const,
  platformTenants: () => ["platform", "tenants"] as const,
  panelSession: () => ["panel-session"] as const,
};

export function areaQueryKey(areaId: AreaId, tenantId: string) {
  switch (areaId) {
    case "dashboard":
      return queryKeys.dashboard(tenantId);
    case "conversations":
      return queryKeys.conversations(tenantId);
    case "contacts":
      return queryKeys.contacts(tenantId);
    case "teams":
      return queryKeys.teams(tenantId);
    case "reports":
      return queryKeys.reports(tenantId);
    case "settings":
      return queryKeys.settings(tenantId);
  }
}

export function signedOutQueryKey(areaId: AreaId) {
  return ["session", "signed-out", areaId] as const;
}
