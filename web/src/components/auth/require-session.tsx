import type { ReactNode } from "react";
import { Navigate } from "react-router";
import { useSessionStore } from "@/stores/session-store";

export function RequireSession({ children }: { children: ReactNode }) {
  const principal = useSessionStore((state) => state.principal);
  if (!principal) return <Navigate to="/entrar" replace />;
  return children;
}
