import { useEffect, useRef, useState } from "react";
import * as api from "@/api/presence";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";

/** "2 abertas, 1 aguardando · 1 adiada" — snoozed conversations do not use a slot. */
export function occupancyDetail(m: { open?: number; waiting?: number; snoozed?: number }) {
  const parts = [`${m.open ?? 0} ${m.open === 1 ? "aberta" : "abertas"}`, `${m.waiting ?? 0} aguardando`];
  return ` (${parts.join(", ")}${m.snoozed ? ` · ${m.snoozed} ${m.snoozed === 1 ? "adiada" : "adiadas"}` : ""})`;
}
export function presenceScope(p: PanelPrincipal) { return JSON.stringify([p.tenantId, p.userId, p.membershipId, p.sessionId, p.role]); }
export function OperatorPresence() {
  const principal = useSessionStore(s => s.principal);
  if (!principal || principal.mfaEnrollmentRequired || !["admin", "supervisor", "agent"].includes(principal.role)) return null;
  return <PresenceBody key={presenceScope(principal)} principal={principal} />;
}
function PresenceBody({ principal }: { principal: PanelPrincipal }) {
  // A UUID per mounted tab, never shared through local/session storage.
  const [snapshot, setSnapshot] = useState<api.PresenceSnapshot | null>(null);
  const [occupied, setOccupied] = useState<{ occupied: number; capacity: number; open?: number; waiting?: number; snoozed?: number } | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => {
    const active = new AbortController(); controller.current = active;
 const tab = crypto.randomUUID();
    let running = false;
    const same = () => { const p = useSessionStore.getState().principal; return p && !p.mfaEnrollmentRequired && presenceScope(p) === presenceScope(principal); };
    async function tick() {
      if (active.signal.aborted || !same() || running) return;
      running = true;
      try {
        const value = await api.heartbeat(tab, active.signal);
        if (!active.signal.aborted && same()) { setSnapshot(value); setError(""); }
        const slots = await api.occupancy(undefined, active.signal);
        if (!active.signal.aborted && same()) setOccupied(slots.members?.find(m => m.membership_id === principal.membershipId) ?? null);
      } catch { if (!active.signal.aborted && same()) setError("Presença indisponível. A disponibilidade não foi confirmada."); }
      finally { running = false; }
    }
    void tick();
    const timer = window.setInterval(() => void tick(), 10_000);
    const stop = () => { active.abort(); window.clearInterval(timer); };
    const unsubscribe = useSessionStore.subscribe(() => { if (!same()) stop(); });
    // pagehide covers navigation/closing; another tab retains its own heartbeat.
    const leave = () => { stop(); void api.closeTab(tab).catch(() => undefined); };
    const resume = () => { /* A bfcache restoration remount is not guaranteed. */ window.location.reload(); };
    const pageShow = (event: PageTransitionEvent) => { if (event.persisted) resume(); };
    window.addEventListener("pagehide", leave); window.addEventListener("pageshow", pageShow);
    return () => { stop(); unsubscribe(); window.removeEventListener("pagehide", leave); window.removeEventListener("pageshow", pageShow); if (same()) void api.closeTab(tab).catch(() => undefined); };
  }, [principal]);
  async function change(state: "available" | "busy") {
    const active = controller.current; if (!active || active.signal.aborted) return;
    setBusy(true);
    try { const value = await api.manual(state, active.signal); if (!active.signal.aborted) { setSnapshot(value); setError(""); } }
    catch { if (!active.signal.aborted) setError("Não foi possível alterar a presença."); }
    finally { if (!active.signal.aborted) setBusy(false); }
  }
  return <section aria-label="Minha presença" className="operator-presence">
    <label>Minha presença <select aria-label="Minha presença" value={snapshot?.manual ?? "available"} disabled={!snapshot || busy} onChange={e => void change(e.target.value as "available" | "busy")}><option value="available">Disponível</option><option value="busy">Ocupado</option></select></label>
    <p role="status">{error ? "Presença não confirmada" : snapshot?.connected ? "Conectado" : "Verificando presença…"}{occupied && ` · Vagas: ${occupied.occupied}/${occupied.capacity}`}{occupied && occupancyDetail(occupied)}</p>
    {error && <p role="alert">{error}</p>}
  </section>;
}
