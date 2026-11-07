import { Menu, X, LogOut, Headphones } from "lucide-react";
import { useEffect, useState, type MouseEvent } from "react";
import { Link, Navigate, Outlet, useLocation } from "react-router";
import { useEndSession } from "@/components/auth/use-end-session";
import { OperatorPresence } from "@/features/presence/operator-presence";
import { ProviderMark } from "@/features/branding/provider-mark";
import { useProviderBrand } from "@/features/branding/use-provider-brand";
import { AreaNav } from "@/components/layout/area-nav";
import { areas } from "@/lib/areas";
import { useSessionStore } from "@/stores/session-store";

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
  const [menuOpen, setMenuOpen] = useState(false);
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
    <div className="app-shell" data-area={area?.id ?? "settings"}>
      <a
        href="#conteudo"
        onClick={skipToContent}
        className="absolute top-4 left-4 z-50 -translate-y-24 rounded-lg bg-foreground px-3 py-2 text-sm font-medium text-background focus-visible:translate-y-0 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground"
      >
        Ir para o conteúdo
      </a>
      <div className="app-layout">
        <aside id="app-navigation" className="app-sidebar" data-open={menuOpen}>
          <ProviderMark />
          <div onClick={(event) => { if ((event.target as HTMLElement).closest("a")) setMenuOpen(false); }}><AreaNav /></div>
          <div className="sidebar-bottom"><OperatorPresence /><p className="sidebar-caption"><Headphones size={14} aria-hidden="true" /> Central de atendimento</p></div>
        </aside>
        <div className="app-body">
          <header className="app-topbar">
            <button type="button" className="mobile-menu icon-button" aria-label={menuOpen ? "Fechar menu" : "Abrir menu"} aria-expanded={menuOpen} aria-controls="app-navigation" onClick={() => setMenuOpen(!menuOpen)}>{menuOpen ? <X size={20} /> : <Menu size={20} />}</button>
            <div className="topbar-location"><span>{brand.name}</span><span aria-hidden="true">/</span><strong>{heading}</strong></div>
            <div className="topbar-session">{displayName && <span aria-label="Nome desta aba">{displayName}</span>}
            {principal ? (
              <button
                type="button"
                className="session-exit"
                onClick={() => {
                  void endSession();
                }}
              >
                <LogOut size={15} aria-hidden="true" /> Sair
              </button>
            ) : (
              <Link
                to="/entrar"
                className="mt-2 inline-flex h-11 items-center rounded-lg px-3 text-sm font-medium text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-foreground"
              >
                Entrar
              </Link>
            )}
            </div>
          </header>
          <main
            id="conteudo"
            tabIndex={-1}
            className="app-content"
          >
            <Outlet />
          </main>
        </div>
      </div>
    </div>
  );
}
