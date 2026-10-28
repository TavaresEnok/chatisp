import { create } from "zustand";

type SessionState = {
  tenantId: string | null;
  displayName: string;
  setTenantId: (tenantId: string | null) => void;
  setDisplayName: (displayName: string) => void;
  clearSession: () => void;
};

export const useSessionStore = create<SessionState>()((set) => ({
  tenantId: null,
  displayName: "",
  setTenantId: (tenantId) => {
    set({ tenantId, displayName: "" });
  },
  setDisplayName: (displayName) => {
    set({ displayName });
  },
  clearSession: () => {
    set({ tenantId: null, displayName: "" });
  },
}));
