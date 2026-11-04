import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { listHoursInboxes, listHoursSectors } from "@/api/businesshours";
import { distribution, setAuto, sectorPresence } from "@/api/distribution";
import { directory, type DirectoryEntry } from "@/api/directory";
import { occupancy } from "@/api/presence";
import { occupancyDetail, presenceScope } from "@/features/presence/operator-presence";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";

export function DistributionSettings({ principal }: { principal: PanelPrincipal }) {
  const [open, setOpen] = useState(false);
  if (!open) return <button type="button" onClick={() => setOpen(true)}>Mostrar distribuição e ocupação</button>;
  return <DistributionBody key={presenceScope(principal)} principal={principal} />;
}
function DistributionBody({ principal }: { principal: PanelPrincipal }) {
  const [sector, setSector] = useState(""); const [inbox, setInbox] = useState("");
  const scope = presenceScope(principal);
  const sectors = useQuery({ queryKey: ["m5ops", scope, "sectors"], queryFn: ({ signal }) => listHoursSectors(signal), retry: false });
  const boxes = useQuery({ queryKey: ["m5ops", scope, "inboxes"], queryFn: ({ signal }) => listHoursInboxes(signal), retry: false });
  return <section aria-label="Distribuição e ocupação"><h2>Distribuição e ocupação</h2>
    <label>Setor <select value={sector} onChange={e => setSector(e.target.value)}><option value="">Selecione</option>{sectors.data?.sectors?.filter(s => s.active).map(s => <option value={s.id} key={s.id}>{s.name}</option>)}</select></label>
    <label>Caixa da fila <select value={inbox} onChange={e => setInbox(e.target.value)}><option value="">Selecione</option>{boxes.data?.inboxes?.map(b => <option value={b.id} key={b.id}>{b.name}</option>)}</select></label>
    {(sectors.isError || boxes.isError) && <p role="alert">Não foi possível listar setores ou caixas.</p>}
    {sector && inbox && <SectorOperations key={`${scope}:${sector}:${inbox}`} principal={principal} sector={sector} inbox={inbox} />}
  </section>;
}
function SectorOperations({ principal, sector, inbox }: { principal: PanelPrincipal; sector: string; inbox: string }) {
  const [view, setView] = useState<Awaited<ReturnType<typeof distribution>> | null>(null);
  const [slots, setSlots] = useState<Awaited<ReturnType<typeof occupancy>> | null>(null);
  const [agents, setAgents] = useState<Awaited<ReturnType<typeof sectorPresence>> | null>(null);
  const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
 const [operators, setOperators] = useState<DirectoryEntry[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    let running = false;
    async function refresh() {
      if (running || controller.signal.aborted) return; running = true;
      try {
        const [next, capacity, presence, people] = await Promise.all([distribution(sector, inbox, controller.signal), occupancy(sector, controller.signal), sectorPresence(sector, controller.signal), directory(inbox, controller.signal)]);
        if (!controller.signal.aborted) { setView(next); setSlots(capacity); setAgents(presence); setOperators(people); setError(""); }
      } catch { if (!controller.signal.aborted) { setView(null); setSlots(null); setAgents(null); setError("Não foi possível consultar o setor."); } }
      finally { running = false; }
    }
    void refresh(); const timer = window.setInterval(() => void refresh(), 10_000);
    const unsubscribe = useSessionStore.subscribe(() => { const p = useSessionStore.getState().principal; if (!p || presenceScope(p) !== presenceScope(principal)) { controller.abort(); clearInterval(timer); } });
    return () => { controller.abort(); clearInterval(timer); unsubscribe(); };
  }, [principal, sector, inbox]);
  async function toggle() {
    if (!view || busy) return; const scope = presenceScope(principal); setBusy(true); setError("");
    try { const next = await setAuto(sector, view.version, !view.enabled); const current = useSessionStore.getState().principal; if (current && presenceScope(current) === scope) setView(next); }
    catch { setError("Não foi possível salvar. Atualize a consulta antes de tentar novamente."); setView(null); }
    finally { setBusy(false); }
  }
  const late = view?.items?.filter((item) => item.alert).length ?? 0;
  return <div>{view && <><p>Atribuição automática: {view.enabled ? "Ligada" : "Desligada"}</p><p>Conversas na fila: {view.items?.length ?? 0}</p>{late > 0 && <p>{late === 1 ? "1 conversa na fila passou do tempo de espera." : `${late} conversas na fila passaram do tempo de espera.`}</p>}{principal.role === "admin" && <button type="button" disabled={busy} onClick={() => void toggle()}>{view.enabled ? "Desligar" : "Ligar"} atribuição automática</button>}</>}
    {slots && <ul aria-label="Ocupação do setor">{slots.members?.map(m => { const presence = agents?.agents?.find(a => a.membership_id === m.membership_id); return <li key={m.membership_id}>{operators.find(o => o.membership_id === m.membership_id)?.label ?? "Atendente de outra caixa"}: {m.occupied}/{m.capacity} vagas{occupancyDetail(m)} · {presence?.state === "available" ? "Disponível" : presence?.state === "busy" ? "Ocupado" : "Offline"}</li>; })}</ul>}
    {error && <p role="alert">{error}</p>}
  </div>;
}
