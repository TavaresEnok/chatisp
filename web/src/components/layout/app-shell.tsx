import { ChevronsRight, LogOut, Menu, Moon, Sun, X } from "lucide-react";
import { useEffect, useState, type MouseEvent } from "react";
import { Link, Navigate, Outlet, useLocation } from "react-router";
import { useEndSession } from "@/components/auth/use-end-session";
import { OperatorPresence } from "@/features/presence/operator-presence";
import { ProviderMark } from "@/features/branding/provider-mark";
import { useProviderBrand } from "@/features/branding/use-provider-brand";
import { AreaNav } from "@/components/layout/area-nav";
import { NavHandle } from "@/components/layout/nav-handle";
import { areas } from "@/lib/areas";
import { useInterfaceStore } from "@/stores/interface-store";
import { useSessionStore } from "@/stores/session-store";

function skipToContent(event: MouseEvent<HTMLAnchorElement>) {
  const target = document.getElementById("conteudo");
  if (!target) {
    return;
  }
  event.preventDefault();
  target.focus();
}

function isTyping(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName))
  );
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
  const theme = useInterfaceStore((state) => state.theme);
  const toggleTheme = useInterfaceStore((state) => state.toggleTheme);
  const menuExpanded = useInterfaceStore((state) => state.menuExpanded);
  const setMenuExpanded = useInterfaceStore((state) => state.setMenuExpanded);
  const endSession = useEndSession();
  const brand = useProviderBrand();
  const expandLabel = menuExpanded ? "Recolher menu" : "Expandir menu";

  useEffect(() => {
    document.documentElement.lang = "pt-BR";
    document.title = `${heading} — ${brand.name}`;
  }, [heading, brand.name]);

  // A tecla [ abre e fecha o menu lateral, sem capturar a digitação em campos e diálogos.
  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key !== "[" || event.ctrlKey || event.metaKey || event.altKey) return;
      if (isTyping(event.target) || document.querySelector("dialog[open], [role=dialog]")) return;
      event.preventDefault();
      const state = useInterfaceStore.getState();
      state.setMenuExpanded(!state.menuExpanded);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

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
      <header className="app-topbar">
        <button
          type="button"
          className="mobile-menu icon-button"
          aria-label={menuOpen ? "Fechar menu" : "Abrir menu"}
          aria-expanded={menuOpen}
          aria-controls="app-navigation"
          onClick={() => setMenuOpen(!menuOpen)}
        >
          {menuOpen ? <X size={18} /> : <Menu size={18} />}
        </button>
        <ProviderMark />
        <div className="topbar-location">
          <span>{brand.name}</span>
          <span aria-hidden="true">/</span>
          <strong>{heading}</strong>
        </div>
        <div className="topbar-session">
          <OperatorPresence />
          <button
            type="button"
            className="icon-button theme-toggle"
            aria-label={theme === "dark" ? "Usar tema claro" : "Usar tema escuro"}
            title={theme === "dark" ? "Usar tema claro" : "Usar tema escuro"}
            onClick={toggleTheme}
          >
            {theme === "dark" ? <Sun size={16} aria-hidden="true" /> : <Moon size={16} aria-hidden="true" />}
          </button>
          {displayName && <span aria-label="Nome desta aba">{displayName}</span>}
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
              className="session-exit"
            >
              Entrar
            </Link>
          )}
        </div>
      </header>
      <div className="app-layout">
        <NavHandle />
        <aside id="app-navigation" className="app-sidebar" data-open={menuOpen}>
          <div
            className="app-sidebar-nav"
            onClick={(event) => {
              if ((event.target as HTMLElement).closest("a")) setMenuOpen(false);
            }}
          >
            <AreaNav />
          </div>
          <button
            type="button"
            className="rail-item menu-toggle"
            aria-expanded={menuExpanded}
            aria-controls="app-navigation"
            aria-label={expandLabel}
            title={`${expandLabel} ( [ )`}
            onClick={() => setMenuExpanded(!menuExpanded)}
          >
            <ChevronsRight aria-hidden="true" />
            <span className="rail-label">Recolher menu</span>
            <kbd>[</kbd>
          </button>
        </aside>
        {menuOpen && (
          <div
            className="app-sidebar-backdrop"
            aria-hidden="true"
            onClick={() => setMenuOpen(false)}
          />
        )}
        <main id="conteudo" tabIndex={-1} className="app-content">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
