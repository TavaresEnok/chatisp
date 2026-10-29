import { useEffect } from "react";
import { useLocation, useNavigate } from "react-router";
import { useSessionStore } from "@/stores/session-store";

export function SessionLossRedirect() {
  const endedReason = useSessionStore((state) => state.endedReason);
  const location = useLocation();
  const navigate = useNavigate();

  useEffect(() => {
    if (endedReason && location.pathname !== "/entrar") {
      navigate("/entrar", { replace: true });
    }
  }, [endedReason, location.pathname, navigate]);

  return null;
}
