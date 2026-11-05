import { directory } from "@/api/directory";
import { listHoursInboxes } from "@/api/businesshours";
import { DistributionSettings } from "@/features/distribution/distribution-settings";
import { AutomationSettings } from "@/features/automation/automation-settings";
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
import { capacity } from "@/api/assignment";
import { CannedSettings } from "@/features/cannedreplies/canned-settings";
import { HoursSettings } from "@/features/businesshours/hours-settings";
import { EmbedSettings } from "@/features/embed/embed-settings";
import { AuditLog } from "@/features/audit/audit-log";
import { OpsAlerts } from "@/features/opsalert/ops-alerts";
import { IdentityEditor } from "@/features/branding/identity-editor";
import { RetentionPanel } from "@/features/retention/retention-panel";
import { IntegrationsPanel } from "@/features/integrations/integrations-panel";
import { canManageIntegrations } from "@/features/integrations/rules";
import { useState } from "react";

export function SettingsPage() {
  const principal = useSessionStore((state) => state.principal);

  return (
    <AreaPage areaId="settings">
      <SettingsSections />
 {principal && !principal.mfaEnrollmentRequired && ["admin", "supervisor"].includes(principal.role) && <DistributionSettings key={`distribution:${principal.sessionId}`} principal={principal} />}
      {principal && !principal.mfaEnrollmentRequired && ["admin", "supervisor"].includes(principal.role) && <AutomationSettings key={`automation:${principal.sessionId}`} principal={principal} />}
      {principal && !principal.mfaEnrollmentRequired && ["admin", "supervisor", "agent"].includes(principal.role) && <Preferences key={`preferences:${JSON.stringify(queryKeys.notificationPreferences(principal))}`} principal={principal} />}
      {principal?.role === "admin" && !principal.mfaEnrollmentRequired ? <Link to="/configuracoes/canais">Caixas Meta</Link> : null}
      {principal?.role === "admin" && !principal.mfaEnrollmentRequired ? <AuditLog key={`audit:${principal.sessionId}`} principal={principal} /> : null}
      {principal?.role === "admin" && !principal.mfaEnrollmentRequired ? <OpsAlerts key={`ops:${principal.sessionId}`} sessionId={principal.sessionId} /> : null}
      {principal?.role === "admin" && !principal.mfaEnrollmentRequired ? <IdentityEditor key={`identity:${principal.tenantId}:${principal.sessionId}`} /> : null}
      {principal?.role === "admin" && !principal.mfaEnrollmentRequired ? <RetentionPanel key={`retention:${principal.membershipId}`} /> : null}
      {canManageIntegrations(principal) ? <IntegrationsPanel key={`integrations:${principal.sessionId}`} principal={principal} /> : null}
      {principal && !principal.mfaEnrollmentRequired && ["admin", "supervisor"].includes(principal.role) && <Capacity key={`capacity:${principal.sessionId}`} principal={principal} />}
      {principal && !principal.mfaEnrollmentRequired && ["admin", "supervisor", "agent"].includes(principal.role) && <CannedSettings key={`canned:${principal.membershipId}`} principal={principal} />}
      {principal && !principal.mfaEnrollmentRequired && ["admin", "supervisor"].includes(principal.role) && <HoursSettings key={`hours:${principal.membershipId}`} principal={principal} />}
      {principal && !principal.mfaEnrollmentRequired && ["admin", "supervisor"].includes(principal.role) && <EmbedSettings key={`embed:${principal.membershipId}`} principal={principal} />}
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

function Capacity({ principal }: { principal: PanelPrincipal }) {
  const [inbox, setInbox] = useState("");
 const boxes = useQuery({queryKey:["m5ops",principal.sessionId,"capacity-inboxes"],queryFn:({signal})=>listHoursInboxes(signal),retry:false});
 const people = useQuery({queryKey:["m5ops",principal.sessionId,"capacity-directory",inbox],queryFn:({signal})=>directory(inbox,signal),enabled:!!inbox,retry:false});
  const [member, setMember] = useState("");
  const [limit, setLimit] = useState(20);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function save() {
    setBusy(true); setMessage("");
    try { await capacity(member, limit); setMessage("Capacidade atualizada."); }
    catch { setMessage("Não foi possível atualizar a capacidade."); }
    finally { setBusy(false); }
  }
  return <section aria-label="Capacidade de atendimento"><h2>Capacidade de atendimento</h2>
    <label>Caixa dos atendentes <select value={inbox} onChange={e => { setInbox(e.target.value); setMember(""); }}><option value="">Selecione</option>{boxes.data?.inboxes?.map(b => <option key={b.id} value={b.id}>{b.name}</option>)}</select></label>
 <label>Atendente <select value={member} onChange={e => setMember(e.target.value)}><option value="">Selecione</option>{people.data?.map(p => <option key={p.membership_id} value={p.membership_id}>{p.label}</option>)}</select></label>
 {(boxes.isError || people.isError) && <p role="alert">Não foi possível listar os atendentes autorizados.</p>}
    <label>Capacidade <input type="number" min={1} value={limit} onChange={e => setLimit(Number(e.target.value))} /></label>
    <button type="button" disabled={!member || !Number.isInteger(limit) || limit < 1 || busy} onClick={() => void save()}>Salvar capacidade</button>
    {message && <p role="status">{message}</p>}
  </section>;
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
