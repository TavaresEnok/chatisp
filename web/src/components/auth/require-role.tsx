import type { ReactNode } from "react";
import { Link } from "react-router";
import { useSessionStore } from "@/stores/session-store";

type RequireRoleProps = {
  role: "admin";
  children: ReactNode;
};

export function RequireRole({ role, children }: RequireRoleProps) {
  const principal = useSessionStore((state) => state.principal);

  if (!principal) {
    return (
      <p className="text-sm">
        Entre para continuar.{" "}
        <Link to="/entrar" className="font-medium underline-offset-4 hover:underline">
          Entrar
        </Link>
      </p>
    );
  }

  if (principal.role !== role) {
    return (
      <p role="alert" className="text-sm">
        Seu papel não permite esta área. O servidor continua autorizando cada ação.
      </p>
    );
  }

  return children;
}
