import { useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as assignment from "@/api/assignment";
import { AttendanceError } from "@/api/attendance";
import type { PanelPrincipal } from "@/stores/session-store";

type Action = "member" | "sector" | "unassign";
type Props = { principal: PanelPrincipal; inbox: string; conversation: string; version: number; onChanged: (sector: boolean) => void; labels?: ReadonlyMap<string, string> };
export function AssignmentPanel(props: Props) {
  const { principal: p, inbox, conversation } = props;
  return <AssignmentSession key={JSON.stringify([p.tenantId, p.userId, p.sessionId, p.role, p.membershipId, inbox, conversation])} {...props} />;
}
function AssignmentSession({ principal, inbox, conversation, version, onChanged, labels }: Props) {
  const client = useQueryClient();
  const generation = useRef<object | null>(null);
  useEffect(() => { generation.current = {}; return () => { generation.current = null; }; }, []);
  const scope = [principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId, inbox, conversation];
  const [action, setAction] = useState<Action | null>(null);
  const [target, setTarget] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [held, setHeld] = useState(false);
  const intent = useRef<{ action: Action; target: string; version: number; id: string } | null>(null);
  function remember(next: { action: Action; target: string; version: number; id: string } | null) {
    intent.current = next;
    setHeld(next !== null);
  }
  const candidates = useQuery({ queryKey: ["m3b-candidates", ...scope], queryFn: () => assignment.candidates(inbox, conversation), enabled: action === "member", retry: false });
  const sectors = useQuery({ queryKey: ["m3b-sectors", ...scope], queryFn: ({ signal }) => assignment.transferSectors(inbox, conversation, signal), enabled: action === "sector", retry: false });
  async function submit() {
    if (!action || busy || (action !== "unassign" && !target)) return;
    const current = intent.current ?? { action, target, version, id: crypto.randomUUID() };
    if (current.action !== action || current.target !== target) { setMessage("Repita a ação anterior; a resposta não chegou."); return; }
    remember(current); setBusy(true); setMessage("");
    const started = generation.current;
    const isCurrent = () => started !== null && generation.current === started;
    try {
      await assignment.assign(inbox, conversation, action, { expected_version: current.version, client_request_id: current.id, ...(action === "unassign" ? {} : { target_id: target }) });
      // Reconcile only existing queries for the operation's original box.
      void client.invalidateQueries({ queryKey: ["attendance", ...scope.slice(0, 6)] });
      void client.invalidateQueries({ queryKey: ["m3b-queue", ...scope.slice(0, 6)] });
      if (!isCurrent()) return;
      remember(null); setAction(null); onChanged(action === "sector");
    } catch (error) {
      if (!isCurrent()) return;
      if (error instanceof AttendanceError) { remember(null); if (error.status === 409) onChanged(false); }
      setMessage(error instanceof AttendanceError ? error.message : "Resposta perdida; repita a mesma ação.");
    } finally { if (isCurrent()) setBusy(false); }
  }
  return <section aria-label="Atribuição" className="space-x-2">
    {(["member", "sector", "unassign"] as const).map(item => <button type="button" key={item} onClick={() => { if (intent.current) return; setAction(item); setTarget(""); setMessage(""); }}>{item === "member" ? "Transferir para atendente" : item === "sector" ? "Transferir para setor" : "Devolver à fila"}</button>)}
    {action && <div role="dialog" aria-modal="true" aria-label="Confirmar atribuição" className="border p-3">
      <h2>{action === "member" ? "Transferir para atendente" : action === "sector" ? "Transferir para setor" : "Devolver à fila"}</h2>
      {action === "member" && <label>Atendente <select value={target} onChange={e => { if (!held) setTarget(e.target.value); }} disabled={busy || held}><option value="">Selecione</option>{(candidates.data?.candidates ?? []).map(c => <option key={c.membership_id} value={c.membership_id} disabled={c.assigned >= c.capacity}>{labels?.get(c.membership_id) ?? "Atendente indisponível"} — {c.assigned}/{c.capacity}{c.assigned >= c.capacity ? " (sem vaga)" : ""}</option>)}</select>{candidates.isError && <p role="alert">Não foi possível carregar candidatos.</p>}</label>}
      {action === "sector" && <label>Setor <select value={target} onChange={e => { if (!held) setTarget(e.target.value); }} disabled={busy || held}><option value="">Selecione</option>{(sectors.data ?? []).map(s => <option key={s.id} value={s.id}>{s.name}</option>)}</select>{sectors.isError && <p role="alert">Não foi possível carregar setores.</p>}</label>}
      <button type="button" disabled={busy || (action !== "unassign" && !target)} onClick={() => void submit()}>Confirmar</button>
      <button type="button" disabled={busy || held} onClick={() => { if (!held) setAction(null); }}>Cancelar</button>
      {message && <p role="status">{message}</p>}
    </div>}
  </section>;
}
