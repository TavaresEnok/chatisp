import { Navigate } from "react-router";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";
import { ConversationWorkspace } from "@/features/conversation/conversation-workspace";
import { useAttendance } from "@/features/conversation/use-attendance";

export function ConversationsPage() {
  const principal = useSessionStore((s) => s.principal);
  if (!principal || principal.mfaEnrollmentRequired) return <Navigate to="/entrar" replace />;
  if (!["admin", "supervisor", "agent"].includes(principal.role)) return <p>Acesso restrito ao atendimento.</p>;
  return <AttendancePage key={`${principal.tenantId}:${principal.userId}:${principal.sessionId}`} principal={principal} />;
}
function AttendancePage({ principal }: { principal: PanelPrincipal }) {
  const a = useAttendance(principal);
  return <section className="space-y-4"><h1>Conversas</h1>
    {a.inbox && a.realtime !== "connected" && <p role="status">{a.realtime === "degraded" ? "Tempo real limitado para esta caixa; atualize os dados manualmente." : a.realtime === "resync" ? "Ressincronizando atendimento…" : "Tempo real desconectado; tentando reconectar."}</p>}
    {a.boxes.isPending ? <p>Carregando caixas…</p> : a.boxes.isError ? <p role="alert">Não foi possível carregar as caixas.</p> : <label>Caixa <select value={a.inbox ?? ""} onChange={(e) => a.selectInbox(e.target.value)}><option value="">Selecione uma caixa</option>{(a.boxes.data.inboxes ?? []).map((box) => <option value={box.id} key={box.id}>{box.name} — {box.channel_status}</option>)}</select>{!a.boxes.data.inboxes?.length && <p>Nenhuma caixa disponível.</p>}</label>}
    {a.inbox && <ConversationWorkspace key={a.inbox} localQueue scope={{ tenantId: principal.tenantId, userId: `${principal.userId}:${principal.sessionId}` }} listStatus={a.list.isPending ? "loading" : a.list.isError ? "error" : "ready"} conversations={a.cards} detailConversationId={a.selected} detailStatus={a.detail.isPending ? "loading" : a.detail.isError ? "error" : "ready"} entries={a.entries} onSelect={a.setSelected} onAssume={a.assume} onSend={a.send} onClose={a.close} onReopen={a.reopen} onFarewell={() => ({ outcome: "failed", detail: "A despedida faz parte do encerramento." })} />}
  </section>;
}
