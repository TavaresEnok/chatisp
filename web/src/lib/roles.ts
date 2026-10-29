export const providerRoles = ["admin", "supervisor", "agent"] as const;

export type ProviderRole = (typeof providerRoles)[number];

const roleLabels: Record<ProviderRole, string> = {
  admin: "Administrador",
  supervisor: "Supervisor",
  agent: "Atendente",
};

export function isProviderRole(role: string): role is ProviderRole {
  return providerRoles.some((item) => item === role);
}

export function roleLabel(role: string): string {
  if (isProviderRole(role)) {
    return roleLabels[role];
  }
  return "Papel desconhecido";
}

export function isProviderAdmin(role: string | null | undefined): boolean {
  return role === "admin";
}
