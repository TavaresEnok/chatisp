import { api } from "@/api/client";
import { PanelRequestError, problemMessage } from "@/api/problems";
import type { components } from "@/api/schema";
import type { PanelPrincipal } from "@/stores/session-store";
import { useSessionStore } from "@/stores/session-store";

export type SessionBody = components["schemas"]["SessionBody"];
export type Membership = components["schemas"]["MembershipBody"];
export type SessionInfo = components["schemas"]["SessionInfoBody"];
export type Tenant = components["schemas"]["TenantBody"];
export type Enrollment = components["schemas"]["EnrollmentOutputBody"];
export type ProviderRole = components["schemas"]["InviteInputBody"]["role"];

type CallResult<T> = {
  data?: T;
  error?: unknown;
  response: Response;
};

async function unwrap<T>(result: CallResult<T>): Promise<T> {
  if (!result.response.ok) {
    throw new PanelRequestError(
      problemMessage(result.error, result.response.status),
      result.response.status,
    );
  }
  return result.data as T;
}

export function principalFromSession(body: SessionBody): PanelPrincipal {
  return {
    userId: body.user_id,
    email: body.email,
    tenantId: body.tenant_id,
    membershipId: body.membership_id,
    role: body.role,
    sessionId: body.session_id,
    expiresAt: body.expires_at,
    mfaEnrollmentRequired: body.mfa_enrollment_required,
  };
}

export function rememberSession(body: SessionBody) {
  useSessionStore.getState().applySession(principalFromSession(body), body.csrf_token);
}

export async function login(body: components["schemas"]["LoginInputBody"]) {
  return api.POST("/api/panel/session", { body });
}

export async function acceptInvitation(body: components["schemas"]["AcceptInputBody"]) {
  return api.POST("/api/panel/invitations/accept", { body });
}

export async function requestPasswordReset(email: string) {
  const result = await api.POST("/api/panel/password-resets", { body: { email } });
  return unwrap(result);
}

export async function confirmPasswordReset(body: components["schemas"]["ResetConfirmInputBody"]) {
  const result = await api.POST("/api/panel/password-resets/confirm", { body });
  return unwrap(result);
}

export async function loadCurrentSession(): Promise<SessionBody | null> {
  const result = await api.GET("/api/panel/session");
  if (result.response.status === 401 || result.response.status === 403) {
    return null;
  }
  if (!result.response.ok || !result.data) {
    return null;
  }
  return {
    ...result.data,
    csrf_token: "",
  };
}

export async function listMemberships(): Promise<Membership[]> {
  const result = await api.GET("/api/panel/memberships");
  const body = await unwrap(result);
  return body.memberships ?? [];
}

export async function inviteMember(email: string, role: ProviderRole) {
  const result = await api.POST("/api/panel/invitations", { body: { email, role } });
  return unwrap(result);
}

export async function setMembershipActive(membershipId: string, active: boolean) {
  if (active) {
    const result = await api.POST("/api/panel/memberships/{membership_id}/reactivate", {
      params: { path: { membership_id: membershipId } },
    });
    return unwrap(result);
  }
  const result = await api.POST("/api/panel/memberships/{membership_id}/deactivate", {
    params: { path: { membership_id: membershipId } },
  });
  return unwrap(result);
}

export async function listSessions(): Promise<SessionInfo[]> {
  const result = await api.GET("/api/panel/sessions");
  const body = await unwrap(result);
  return body.sessions ?? [];
}

export async function revokeSession(sessionId: string) {
  const result = await api.POST("/api/panel/sessions/{session_id}/revoke", {
    params: { path: { session_id: sessionId } },
  });
  return unwrap(result);
}

export async function beginMfa(): Promise<Enrollment> {
  const result = await api.POST("/api/panel/mfa/enrollment");
  return unwrap(result);
}

export async function confirmMfa(code: string) {
  const result = await api.POST("/api/panel/mfa/enrollment/confirm", { body: { code } });
  return unwrap(result);
}

export async function endServerSession() {
  const result = await api.DELETE("/api/panel/session");
  return unwrap(result);
}

export type PlatformAccess =
  | { allowed: false }
  | { allowed: true; tenants: Tenant[] };

export async function loadPlatformAccess(): Promise<PlatformAccess> {
  const result = await api.GET("/api/platform/tenants");
  if (result.response.status === 403) {
    return { allowed: false };
  }
  const body = await unwrap(result);
  return { allowed: true, tenants: body.tenants ?? [] };
}

export async function createProvider(timezone: string) {
  const result = await api.POST("/api/platform/tenants", { body: { timezone } });
  return unwrap(result);
}

export async function setProviderSuspended(tenantId: string, suspended: boolean) {
  if (suspended) {
    const result = await api.POST("/api/platform/tenants/{tenant_id}/suspend", {
      params: { path: { tenant_id: tenantId } },
    });
    return unwrap(result);
  }
  const result = await api.POST("/api/platform/tenants/{tenant_id}/reactivate", {
    params: { path: { tenant_id: tenantId } },
  });
  return unwrap(result);
}

export async function inviteProviderAdmin(tenantId: string, email: string) {
  const result = await api.POST("/api/platform/tenants/{tenant_id}/admin-invitations", {
    params: { path: { tenant_id: tenantId } },
    body: { email },
  });
  return unwrap(result);
}
