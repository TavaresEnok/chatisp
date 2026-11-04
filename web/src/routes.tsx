import { Navigate, Route, Routes } from "react-router";
import { AppShell } from "@/components/layout/app-shell";
import { AcceptInvitePage } from "@/pages/accept-invite-page";
import { LoginPage } from "@/pages/login-page";
import { MfaPage } from "@/pages/mfa-page";
import { PasswordResetConfirmPage } from "@/pages/password-reset-confirm-page";
import { PasswordResetPage } from "@/pages/password-reset-page";
import { PlatformTenantsPage } from "@/pages/platform-tenants-page";
import { ProviderUsersPage } from "@/pages/provider-users-page";
import { SettingsPage } from "@/pages/settings-page";
import { ChannelSettingsPage } from "@/pages/channel-settings-page";
import { ConversationsPage } from "@/pages/conversations-page";
import { UnknownPage } from "@/pages/unknown-page";
import { TeamsPage } from "@/pages/teams-page";
import { ContactsPage } from "@/pages/contacts-page";
import { DashboardPage } from "@/pages/dashboard-page";
import { ReportsPage } from "@/pages/reports-page";

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/entrar" element={<LoginPage />} />
      <Route path="/mfa" element={<MfaPage />} />
      <Route path="/convite" element={<AcceptInvitePage />} />
      <Route path="/recuperar-senha" element={<PasswordResetPage />} />
      <Route path="/recuperar-senha/confirmar" element={<PasswordResetConfirmPage />} />
      <Route element={<AppShell />}>
        <Route index element={<Navigate to="/painel" replace />} />
        <Route path="painel" element={<DashboardPage />} />
        <Route path="conversas" element={<ConversationsPage />} />
        <Route path="contatos" element={<ContactsPage />} />
        <Route path="equipes" element={<TeamsPage />} />
        <Route path="relatorios" element={<ReportsPage />} />
        <Route path="configuracoes" element={<SettingsPage />} />
        <Route path="configuracoes/canais" element={<ChannelSettingsPage />} />
        <Route path="configuracoes/usuarios" element={<ProviderUsersPage />} />
        <Route path="configuracoes/provedores" element={<PlatformTenantsPage />} />
        <Route path="*" element={<UnknownPage />} />
      </Route>
    </Routes>
  );
}
