import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { endServerSession } from "@/api/panel";
import { clearBrowserStorage } from "@/lib/browser-storage";
import { useSessionStore } from "@/stores/session-store";

export function useEndSession() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const clearSession = useSessionStore((state) => state.clearSession);

  return async () => {
    try {
      await endServerSession();
    } catch {
      // A rede pode falhar. O navegador ainda precisa esquecer a sessão.
    }
    clearBrowserStorage();
    clearSession();
    queryClient.clear();
    navigate("/entrar", { replace: true });
  };
}
