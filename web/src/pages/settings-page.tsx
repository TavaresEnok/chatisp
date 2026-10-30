import { Link } from "react-router";
import { AreaPage } from "@/components/layout/area-page";
import { LocalDisplayNameForm } from "@/components/settings/local-display-name-form";
import { PlatformAdmin } from "@/components/settings/platform-admin";
import { ProviderAdmin } from "@/components/settings/provider-admin";
import { SettingsSections } from "@/components/settings/settings-sections";
import { useSessionStore } from "@/stores/session-store";

export function SettingsPage() {
  const principal = useSessionStore((state) => state.principal);

  return (
    <AreaPage areaId="settings">
      <SettingsSections />
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
