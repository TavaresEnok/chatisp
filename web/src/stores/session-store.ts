import { create } from "zustand";
import { setCsrfToken } from "@/api/client";

export type PanelPrincipal = {
  userId: string;
  email: string;
  tenantId: string;
  membershipId: string;
  role: string;
  sessionId: string;
  expiresAt: string;
  mfaEnrollmentRequired: boolean;
};

type SessionState = {
  tenantId: string | null;
  displayName: string;
  principal: PanelPrincipal | null;
  csrfToken: string | null;
  endedReason: "ended" | null;
  setTenantId: (tenantId: string | null) => void;
  setDisplayName: (displayName: string) => void;
  applySession: (principal: PanelPrincipal, csrfToken: string) => void;
  completeEnrollment: () => void;
  clearSession: () => void;
  markSessionEnded: () => void;
};

const signedOut = {
  tenantId: null,
  displayName: "",
  principal: null,
  csrfToken: null,
} as const;

export const useSessionStore = create<SessionState>()((set) => ({
  ...signedOut,
  endedReason: null,
  setTenantId: (tenantId) => {
    set({ tenantId, displayName: "" });
  },
  setDisplayName: (displayName) => {
    set({ displayName });
  },
  applySession: (principal, csrfToken) => {
    setCsrfToken(csrfToken);
    set((state) => {
      const samePrincipal =
        state.principal !== null &&
        state.principal.tenantId === principal.tenantId &&
        state.principal.userId === principal.userId;
      return {
        principal,
        csrfToken: csrfToken.length > 0 ? csrfToken : null,
        endedReason: null,
        tenantId: principal.tenantId,
        displayName: samePrincipal ? state.displayName : "",
      };
    });
  },
  completeEnrollment: () => {
    set((state) => ({
      principal: state.principal
        ? { ...state.principal, mfaEnrollmentRequired: false }
        : null,
    }));
  },
  clearSession: () => {
    setCsrfToken(null);
    set({ ...signedOut, endedReason: null });
  },
  markSessionEnded: () => {
    setCsrfToken(null);
    set({ ...signedOut, endedReason: "ended" });
  },
}));

export function resetSessionStore() {
  setCsrfToken(null);
  useSessionStore.setState({
    tenantId: null,
    displayName: "",
    principal: null,
    csrfToken: null,
    endedReason: null,
  });
}
