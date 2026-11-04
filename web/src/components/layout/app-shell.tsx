import { useEffect, type MouseEvent } from "react";
import { Link, Navigate, Outlet, useLocation } from "react-router";
import { useEndSession } from "@/components/auth/use-end-session";
import { OperatorPresence } from "@/features/presence/operator-presence";
import { ProviderMark } from "@/features/branding/provider-mark";
import { useProviderBrand } from "@/features/branding/use-provider-brand";
import { AreaNav } from "@/components/layout/area-nav";
import { areas } from "@/lib/areas";
import { useSessionStore } from "@/stores/session-store";

function sessionSummary(tenantId: string | null, displayName: string): string {
  const session =
    tenantId === null
      ? "Sessão do servidor ainda não ligada."
      : "Provedor em sessão.";
  if (displayName.length === 0) {
    return `${session} O nome desta aba ainda não foi definido.`;
  }
  return `${session} Nome nesta aba: ${displayName}.`;
}

function skipToContent(event: MouseEvent<HTMLAnchorElement>) {
  const target = document.getElementById("conteudo");
  if (!target) {
    return;
  }
  event.preventDefault();
  target.focus();
}

export function AppShell() {
  const location = useLocation();
  const area = areas.find((item) => item.path === location.pathname);
  const heading =
    area?.label ??
    (location.pathname.startsWith("/configuracoes")
      ? "Configurações"
      : "Página não encontrada");
  const tenantId = useSessionStore((state) => state.tenantId);
  const displayName = useSessionStore((state) => state.displayName);
  const principal = useSessionStore((state) => state.principal);
  const endSession = useEndSession();
  const brand = useProviderBrand();

  useEffect(() => {
    document.documentElement.lang = "pt-BR";
    document.title = `${heading} — ${brand.name}`;
  }, [heading, brand.name]);

  if (principal?.mfaEnrollmentRequired) {
    return <Navigate to="/mfa" replace />;
  }

  return (
    <div className="min-h-svh bg-background text-foreground">
      <a
        href="#conteudo"
        onClick={skipToContent}
        className="absolute top-4 left-4 z-50 -translate-y-24 rounded-lg bg-foreground px-3 py-2 text-sm font-medium text-background focus-visible:translate-y-0 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground"
      >
        Ir para o conteúdo
      </a>
      <div className="md:grid md:min-h-svh md:grid-cols-[16rem_minmax(0,1fr)]">
        <aside className="border-b border-sidebar-border bg-sidebar text-sidebar-foreground md:border-r md:border-b-0">
          <ProviderMark />
          <AreaNav />
 <OperatorPresence />
        </aside>
        <div className="flex min-w-0 flex-col">
          <header className="border-b px-4 py-3 md:px-8">
            <p className="min-w-0 text-sm break-words text-foreground">
              {sessionSummary(tenantId, displayName)}
            </p>
            {principal ? (
              <button
                type="button"
                className="mt-2 h-11 rounded-lg px-3 text-sm font-medium text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground"
                onClick={() => {
                  void endSession();
                }}
              >
                Sair
              </button>
            ) : (
              <Link
                to="/entrar"
                className="mt-2 inline-flex h-11 items-center rounded-lg px-3 text-sm font-medium text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground"
              >
                Entrar
              </Link>
            )}
          </header>
          <main
            id="conteudo"
            tabIndex={-1}
            className="flex-1 px-4 py-6 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-foreground md:px-8"
          >
            <Outlet />
          </main>
        </div>
      </div>
    </div>
  );
}
