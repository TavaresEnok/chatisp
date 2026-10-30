import { Link } from "react-router";
import { AreaPage } from "@/components/layout/area-page";
import { LocalDisplayNameForm } from "@/components/settings/local-display-name-form";
import { PlatformAdmin } from "@/components/settings/platform-admin";
import { ProviderAdmin } from "@/components/settings/provider-admin";
import { SettingsSections } from "@/components/settings/settings-sections";
import { useSessionStore } from "@/stores/session-store";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getPreferences, savePreferences } from "@/api/notification-preferences";
import { NotificationPreferences, type PreferenceValues } from "@/features/notifications/notification-preferences";
import type { PanelPrincipal } from "@/stores/session-store";
import { queryKeys } from "@/lib/query-keys";

export function SettingsPage() {
  const principal = useSessionStore((state) => state.principal);

  return (
    <AreaPage areaId="settings">
      <SettingsSections />
      {principal && !principal.mfaEnrollmentRequired && ["admin", "supervisor", "agent"].includes(principal.role) && <Preferences key={JSON.stringify(queryKeys.notificationPreferences(principal))} principal={principal} />}
      {principal?.role === "admin" && !principal.mfaEnrollmentRequired ? <Link to="/configuracoes/canais">Caixas Meta</Link> : null}
      {principal && !principal.mfaEnrollmentRequired ? (
        <p className="text-sm">
          <Link to="/mfa" className="font-medium underline-offset-4 hover:underline">
            Cadastrar autenticação em duas etapas
          </Link>
        </p>
      ) : null}
      <ProviderAdmin />
      <PlatformAdmin />
      <LocalDisplayNameForm />
    </AreaPage>
  );
}

function Preferences({ principal }: { principal: PanelPrincipal }) {
  const key = queryKeys.notificationPreferences(principal);
  const client = useQueryClient();
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => getPreferences(signal), retry: false });
  async function save(next: PreferenceValues) {
    const currentScope = () => {
      const current = useSessionStore.getState().principal;
      return current && !current.mfaEnrollmentRequired && ["admin", "supervisor", "agent"].includes(current.role) && JSON.stringify(queryKeys.notificationPreferences(current)) === JSON.stringify(key);
    };
    if (!currentScope()) return;
    const controller = new AbortController();
    // Observe the transition synchronously, including leaving and returning before a response.
    const unsubscribe = useSessionStore.subscribe(() => { if (!currentScope()) controller.abort(); });
    try {
      const value = await savePreferences(next, controller.signal);
      if (!controller.signal.aborted && currentScope()) client.setQueryData(key, value);
    } finally { unsubscribe(); }
  }
  return query.isPending ? <p role="status">Carregando preferências de notificações…</p> : query.isError ? <p role="alert">Não foi possível carregar as preferências de notificações.</p> : <NotificationPreferences prefs={query.data} onSave={save} />;
}
