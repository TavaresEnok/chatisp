import { MessagesSquare, Inbox } from "lucide-react";
import { Link, Navigate } from "react-router";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";
import { ConversationWorkspace } from "@/features/conversation/conversation-workspace";
import { useAttendance } from "@/features/conversation/use-attendance";
import { ContactHistory } from "@/features/contacts/contact-history";
import { useQuery } from "@tanstack/react-query";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { getPreferences } from "@/api/notification-preferences";
import { useNotificationEvents } from "@/features/notifications/use-notification-events";
import { queryKeys } from "@/lib/query-keys";
import { QueueControls } from "@/features/queue/queue-controls";
import { useQueue } from "@/features/queue/use-queue";
import { queueCard, queueItem } from "@/features/queue/queue-adapter";
import type { QueueFilterOptions } from "@/features/queue/queue-types";
import { AssignmentPanel } from "@/features/assignment/assignment-panel";
import { SnoozePanel } from "@/features/automation/snooze-panel";
import { AnnotationPanel } from "@/features/annotations/annotation-panel";
import { ExternalPanel } from "@/features/embed/external-panel";
import { useCollision } from "@/features/collision/use-collision";
import { api } from "@/api/client";
import { tags } from "@/api/annotations";
import { AttendanceError } from "@/api/attendance";
import { directory } from "@/api/directory";
import { forgetAttachmentQueries } from "@/features/attachments/cache";
import { forgetPolicyQueries } from "@/features/channelpolicy/cache";

export function ConversationsPage() {
  const principal = useSessionStore((s) => s.principal);
  if (!principal || principal.mfaEnrollmentRequired) return <Navigate to="/entrar" replace />;
  if (!["admin", "supervisor", "agent"].includes(principal.role)) return <p>Acesso restrito ao atendimento.</p>;
  return <AttendancePage key={JSON.stringify([principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId])} principal={principal} />;
}
function AttendancePage({ principal }: { principal: PanelPrincipal }) {
  const a = useAttendance(principal);
  const q = useQueue(principal, a.inbox, a.selectInbox);
  const client = useQueryClient();
  const [workspaceRevision, setWorkspaceRevision] = useState(0);
  const [deniedDirectories, setDeniedDirectories] = useState<ReadonlySet<string>>(() => new Set());
  useEffect(() => {
    const scope = [principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId];
    const prefixes = ["m3b-queue", "m3b-annotations", "m3b-tags", "m3b-candidates", "m3b-sectors", "m3b-directory-sectors", "m3b-directory-tags", "m3b-directory"];
    return () => { for (const prefix of prefixes) { const key = [prefix, ...scope]; void client.cancelQueries({ queryKey: key }); client.removeQueries({ queryKey: key }); } };
  }, [client, principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId]);
  const queueRevoked = q.result.error instanceof AttendanceError && [403, 404].includes(q.result.error.status);
  const scopeBlocked = ["revoked", "checking", "unavailable"].includes(a.realtime);
  const collision = useCollision(a.inbox, !scopeBlocked && !queueRevoked && a.selected && !a.detail.isError ? a.selected : null);
  const directoryAllowed = !!a.inbox && !deniedDirectories.has(a.inbox) && !scopeBlocked && !queueRevoked;
  const directoryKey = [principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId];
  const sectors = useQuery({ queryKey: ["m3b-directory-sectors", ...directoryKey], queryFn: async () => { const result = await api.GET("/api/panel/sectors"); if (!result.data) throw new Error("Setores indisponíveis"); return result.data.sectors ?? []; }, retry: false });
  const operators = useQuery({ queryKey: ["m3b-directory", ...directoryKey, a.inbox ?? ""], queryFn: async ({ signal }) => {
    const inbox = a.inbox!;
    try { return await directory(inbox, signal); }
    catch (error) {
      if (!signal.aborted && error instanceof AttendanceError && [403, 404].includes(error.status)) {
        setDeniedDirectories(previous => new Set([...previous, inbox]));
      }
      throw error;
    }
  }, enabled: directoryAllowed, retry: false });
  const directoryDenied = operators.error instanceof AttendanceError && [403, 404].includes(operators.error.status);
  const operatorEntries = directoryAllowed && !directoryDenied ? operators.data ?? [] : [];
  const labels = new Map(operatorEntries.map(entry => [entry.membership_id, entry.label] as const));
  useEffect(() => {
    if (!directoryAllowed) {
      const key = ["m3b-directory", principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId];
      void client.cancelQueries({ queryKey: key }); client.removeQueries({ queryKey: key });
    }
  }, [directoryAllowed, client, principal]);
  const tagCatalog = useQuery({ queryKey: ["m3b-directory-tags", ...directoryKey], queryFn: tags, retry: false });
  const scope = `${principal.tenantId}:${principal.userId}:${principal.sessionId}`;
  const prefs = useQuery({ queryKey: queryKeys.notificationPreferences(principal), queryFn: ({ signal }) => getPreferences(signal), retry: false });
  useNotificationEvents(a.inbox ? `${scope}:${a.inbox}` : null, prefs.data ?? { sound: false, browser: false, silent: true }, a.notificationBatch, () => {});
  const contactId = !scopeBlocked && !queueRevoked && a.detail.data?.head.id === a.selected && !a.detail.isError ? a.detail.data.head.contact_id : null;
  const rows = q.result.data?.items ?? [];
  const selectedId = a.selected;
  const detailDenied = a.detail.isError && a.detail.error instanceof Error && "status" in a.detail.error && [403, 404].includes(Number(a.detail.error.status));
  const missingFromQueue = q.result.isSuccess && selectedId !== null && !rows.some((row) => row.id === selectedId);
  const selectionInvalid = selectedId !== null && (queueRevoked || a.realtime === "revoked" || missingFromQueue || detailDenied);
  const lossKey = selectionInvalid ? `${selectedId}\0${a.inbox ?? ""}\0${queueRevoked || a.realtime === "revoked" || a.detail.isError ? "purge" : "keep"}` : null;
  const [handledLoss, setHandledLoss] = useState<string | null>(null);
  const [pendingPurge, setPendingPurge] = useState<{ inbox: string | null; selected: string; nonce: number } | null>(null);
  if (lossKey === null) {
    if (handledLoss !== null) setHandledLoss(null);
  } else if (lossKey !== handledLoss && selectedId !== null) {
    setHandledLoss(lossKey);
    setWorkspaceRevision((value) => value + 1);
    if (queueRevoked || a.realtime === "revoked" || a.detail.isError) setPendingPurge(current => ({ inbox: a.inbox, selected: selectedId, nonce: (current?.nonce ?? 0) + 1 }));
    a.setSelected(null);
  }
  useEffect(() => {
    if (!pendingPurge) return;
    const scope = [principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId];
    const history = ["attendance", ...scope, pendingPurge.inbox ?? "", "history", pendingPurge.selected];
    const annotations = ["m3b-annotations", ...scope, pendingPurge.inbox, pendingPurge.selected];
    void client.cancelQueries({ queryKey: history }); client.removeQueries({ queryKey: history });
    void client.cancelQueries({ queryKey: annotations }); client.removeQueries({ queryKey: annotations });
    client.removeQueries({ queryKey: queryKeys.contactHistory(scope) });
    void client.cancelQueries({ queryKey: ["m3b-directory", ...scope] }); client.removeQueries({ queryKey: ["m3b-directory", ...scope] });
    forgetPolicyQueries(client, principal, pendingPurge.inbox ?? undefined);
    forgetAttachmentQueries(client, principal, pendingPurge.inbox ?? undefined, pendingPurge.selected);
  }, [pendingPurge, client, principal]);
  const options: QueueFilterOptions = { status: [{ value: "triage", label: "Triagem" }, { value: "open", label: "Aberta" }, { value: "waiting_customer", label: "Aguardando cliente" }, { value: "closed", label: "Encerrada" }], sector: (sectors.data ?? []).map(s => ({ value: s.id, label: s.name })), assignee: operatorEntries.map(entry => ({ value: entry.membership_id, label: entry.label })), inbox: (a.boxes.data?.inboxes ?? []).map(box => ({ value: box.id, label: box.name })), tag: (tagCatalog.data ?? []).map(t => ({ value: t.id, label: t.name })) };
  function changedSector() {
    const base = ["attendance", ...directoryKey];
    void client.cancelQueries({ queryKey: [...base, a.inbox ?? ""] });
    void client.cancelQueries({ queryKey: ["m3b-queue", ...directoryKey] });
    client.removeQueries({ queryKey: ["m3b-queue", ...directoryKey] });
    client.removeQueries({ queryKey: [...base, a.inbox ?? "", "history", a.selected ?? ""] });
    for (const prefix of ["m3b-annotations", "m3b-candidates", "m3b-sectors"]) {
      const key = [prefix, ...directoryKey, a.inbox, a.selected];
      void client.cancelQueries({ queryKey: key }); client.removeQueries({ queryKey: key });
    }
    client.removeQueries({ queryKey: queryKeys.contactHistory(directoryKey) });
    a.setSelected(null); setWorkspaceRevision(x => x + 1); a.refresh();
  }
  return <section className="attendance-page"><div className="attendance-title"><div><p className="eyebrow">CENTRAL DO PROVEDOR</p><h1>Conversas</h1><p>Fila, equipe e histórico em um só lugar.</p></div></div>
    {a.inbox && a.realtime !== "connected" && <p role="status">{a.realtime === "revoked" ? "Acesso à caixa encerrado. Selecione outra caixa." : a.realtime === "checking" ? "Verificando acesso ao atendimento…" : a.realtime === "unavailable" ? "Não foi possível restabelecer o acesso. Selecione novamente a caixa." : a.realtime === "degraded" ? "Tempo real limitado para esta caixa; atualize os dados manualmente." : a.realtime === "resync" ? "Ressincronizando atendimento…" : "Tempo real desconectado; tentando reconectar."}</p>}
    {queueRevoked && <p role="alert">Acesso à fila negado. Selecione outra caixa.</p>}
    {(directoryDenied || (a.inbox && deniedDirectories.has(a.inbox)) || (directoryAllowed && operators.isError)) && <p role="alert">Não foi possível carregar o diretório completo de atendentes.</p>}
    {a.boxes.isPending ? <p>Carregando caixas…</p> : a.boxes.isError ? <p role="alert">Não foi possível carregar as caixas.</p> : <label className="inbox-picker">Caixa <select className="min-w-0 max-w-full" value={a.inbox ?? ""} onChange={(e) => a.selectInbox(e.target.value)}><option value="">Selecione uma caixa</option>{(a.boxes.data.inboxes ?? []).map((box) => <option value={box.id} key={box.id}>{box.name} — {box.channel_status}</option>)}</select>{!a.boxes.data.inboxes?.length && <p>Nenhuma caixa disponível.</p>}</label>}
    {!a.inbox && !a.boxes.isPending && !a.boxes.isError && <div className="attendance-welcome"><div className="welcome-queue"><Inbox size={22} aria-hidden="true" /><h2>Suas caixas de atendimento</h2><p>Escolha uma caixa acima para consultar a fila e os atendimentos da equipe.</p></div><div className="conversation-empty"><span><MessagesSquare size={36} aria-hidden="true" /></span><h2>Tudo pronto para atender</h2><p>{a.boxes.data.inboxes?.length ? "Selecione uma caixa para começar." : "Nenhuma caixa disponível para esta conta."}</p><small>O histórico, os responsáveis e as mensagens aparecerão aqui quando uma caixa estiver disponível.</small>{principal.role === "admin" && <Link className="welcome-settings" to="/configuracoes/canais">Configurar caixas</Link>}</div></div>}
    {a.inbox && !scopeBlocked && !queueRevoked && <ConversationWorkspace key={`${a.inbox}:${workspaceRevision}`} localQueue principal={principal} inboxId={a.inbox} connectorType={(a.boxes.data?.inboxes ?? []).find((box) => box.id === a.inbox)?.connector_type ?? ""} contactId={contactId} conversationVersion={a.detail.data?.head.id === a.selected && !a.detail.isError ? a.detail.data.head.version : null} scope={{ tenantId: principal.tenantId, userId: `${principal.userId}:${principal.sessionId}` }} listStatus={q.result.isPending ? "loading" : q.result.isError ? "error" : "ready"} conversations={rows.map(row => queueCard(row, labels))} detailConversationId={a.selected} detailStatus={a.detail.isPending ? "loading" : a.detail.isError ? "error" : "ready"} entries={a.entries} onSelect={a.setSelected} onAssume={a.assume} onSend={a.send} onClose={a.close} onReopen={a.reopen} onTyping={collision.typing} onFarewell={() => ({ outcome: "failed", detail: "A despedida faz parte do encerramento." })} renderSide={id => <><ExternalPanel key={`embed:${id}`} principal={principal} inboxId={a.inbox!} conversationId={id} />{contactId && <ContactHistory key={contactId} contactId={contactId} scope={[principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId]} />}</>} renderActions={id => a.detail.data?.head.id === id && !a.detail.isError ? <><AssignmentPanel key={`assignment:${id}`} labels={labels} principal={principal} inbox={a.inbox!} conversation={id} version={a.detail.data.head.version} onChanged={sector => sector ? changedSector() : a.refresh()} /><SnoozePanel key={`snooze:${id}`} principal={principal} inbox={a.inbox!} conversation={id} version={a.detail.data.head.version} status={a.detail.data.head.status} onChanged={a.refresh} /><p aria-live="polite">{collision.people.filter(p => p.membership_id !== principal.membershipId).map(p => `${labels.get(p.membership_id) ?? "Outro atendente"} ${p.typing ? "está digitando." : "está visualizando."}`).join(" ")}</p><AnnotationPanel key={`annotations:${id}`} principal={principal} inbox={a.inbox!} conversation={id} cursor={a.detail.data.next_cursor} onChanged={a.refresh} /></> : null} renderQueue={(selected, select) => <div><QueueControls tab={q.tab} onTabChange={q.setTab} search={q.search} onSearchChange={q.setSearch} filters={q.filters} onFiltersChange={q.setFilters} filterOptions={options} order={q.order} onOrderChange={q.setOrder} resultState={q.result.isPending ? "loading" : q.result.isError ? "error" : "ready"} items={rows.map(queueItem)} selectedId={selected} onSelect={select} />{q.cursor && <button type="button" onClick={() => q.setCursor("")}>Primeira página</button>}{q.result.data?.page_cursor && <button type="button" onClick={() => q.setCursor(q.result.data!.page_cursor)}>Próxima página</button>}</div>} />}
  </section>;
}
