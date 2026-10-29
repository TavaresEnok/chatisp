import { useQuery } from "@tanstack/react-query";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { currentCsrfToken, setCsrfToken, setSessionLossHandler } from "@/api/client";
import { loadCurrentSession, principalFromSession } from "@/api/panel";
import { clearBrowserStorage } from "@/lib/browser-storage";
import { queryKeys } from "@/lib/query-keys";
import { useSessionStore } from "@/stores/session-store";

export function SessionLifecycle() {
  const queryClient = useQueryClient();
  const principal = useSessionStore((state) => state.principal);
  const endedReason = useSessionStore((state) => state.endedReason);
  const applySession = useSessionStore((state) => state.applySession);

  useEffect(() => {
    setSessionLossHandler(() => {
      const state = useSessionStore.getState();
      if (state.endedReason === "ended") {
        return;
      }
      const cookieSession = currentCsrfToken();
      if (!state.principal && !state.csrfToken && !cookieSession) {
        return;
      }
      clearBrowserStorage();
      useSessionStore.getState().markSessionEnded();
      queryClient.clear();
    });
    return () => {
      setSessionLossHandler(null);
    };
  }, [queryClient]);

  useQuery({
    queryKey: queryKeys.panelSession(),
    enabled: principal === null && endedReason === null,
    queryFn: async () => {
      if (useSessionStore.getState().endedReason === "ended") {
        return null;
      }
      try {
        const body = await loadCurrentSession();
        if (useSessionStore.getState().endedReason === "ended") {
          return null;
        }
        const csrf = currentCsrfToken();
        if (!body || !csrf) {
          if (!body) {
            setCsrfToken(null);
          }
          return null;
        }
        applySession(principalFromSession({ ...body, csrf_token: csrf }), csrf);
        return body;
      } catch {
        setCsrfToken(null);
        return null;
      }
    },
  });

  return null;
}
