import { useEffect, useRef, useState } from "react";
import * as automation from "@/api/automation";
import { PanelRequestError } from "@/api/problems";
import { presenceScope } from "@/features/presence/operator-presence";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";

type Props = { principal: PanelPrincipal; inbox: string; conversation: string; version: number; status: string; onChanged: () => void };

const presets = [
  { label: "1 hora", minutes: 60 },
  { label: "4 horas", minutes: 240 },
  { label: "Amanhã 9h", minutes: -1 },
];
function presetUntil(minutes: number, now = new Date()) {
  if (minutes > 0) return new Date(now.getTime() + minutes * 60_000);
  const next = new Date(now); next.setDate(next.getDate() + 1); next.setHours(9, 0, 0, 0); return next;
}
/** Value for <input type="datetime-local"> in the browser's local time. */
function localInput(date: Date) {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
const formatter = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" });

/**
 * Snooze ("Adiar") for a conversation under human control: requires a reason
 * and a future instant, releases the slot and returns to the queue (or to the
 * same attendant when eligible) at the instant or on a new customer message.
 */
export function SnoozePanel(props: Props) {
  const { principal: p, inbox, conversation } = props;
  return <SnoozeSession key={JSON.stringify([presenceScope(p), inbox, conversation])} {...props} />;
}

function SnoozeSession({ principal, inbox, conversation, version, status, onChanged }: Props) {
  const [active, setActive] = useState<automation.Snooze | null | undefined>(undefined);
  const [open, setOpen] = useState(false);
  const [until, setUntil] = useState(() => localInput(presetUntil(60)));
  const [reason, setReason] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  // A lost response is retried with the same request id and body.
  const intent = useRef<{ id: string; until: string; reason: string; version: number } | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    automation.getSnooze(inbox, conversation, controller.signal).then(setActive).catch(() => { if (!controller.signal.aborted) setActive(null); });
    return () => controller.abort();
  }, [inbox, conversation, version]);
  const eligible = status === "open" || status === "waiting_customer";
  if (active === undefined || (!eligible && !active)) return null;
  if (active) {
    return <section aria-label="Adiamento" className="text-sm">
      <p role="status">Adiada até {formatter.format(new Date(active.snoozed_until))} · {active.reason}. Volta à fila no horário ou quando o cliente responder.</p>
    </section>;
  }
  const instant = new Date(until);
  const valid = reason.trim().length > 0 && reason.trim().length <= 500 && !Number.isNaN(instant.getTime());
  async function submit() {
    if (!valid || busy) return;
    if (instant.getTime() <= Date.now()) { setMessage("Escolha um horário futuro."); return; }
    const iso = instant.toISOString(), text = reason.trim();
    const current = intent.current && intent.current.until === iso && intent.current.reason === text && intent.current.version === version ? intent.current : { id: crypto.randomUUID(), until: iso, reason: text, version };
    intent.current = current; setBusy(true); setMessage("");
    const scope = presenceScope(principal);
    try {
      const result = await automation.snooze(conversation, { request_id: current.id, inbox_id: inbox, expected_version: current.version, until: current.until, reason: current.reason });
      const now = useSessionStore.getState().principal;
      if (!now || presenceScope(now) !== scope) return;
      intent.current = null; setActive(result); setOpen(false); setReason(""); onChanged();
    } catch (error) {
      if (error instanceof PanelRequestError) {
        intent.current = null;
        setMessage(error.status === 409 ? "A conversa mudou ou já está adiada. Atualize e tente de novo." : error.status === 403 ? "Você não pode adiar esta conversa." : "Não foi possível adiar. Confira o motivo e o horário.");
        if (error.status === 409) onChanged();
      } else setMessage("A resposta não chegou. Repita para confirmar o mesmo adiamento.");
    } finally { setBusy(false); }
  }
  if (!open) return <button type="button" onClick={() => setOpen(true)}>Adiar</button>;
  return <section aria-label="Adiar conversa" className="space-y-1">
    <h2>Adiar conversa</h2>
    <div className="space-x-1">{presets.map(preset => <button type="button" key={preset.label} onClick={() => setUntil(localInput(presetUntil(preset.minutes)))}>{preset.label}</button>)}</div>
    <label>Retornar em <input type="datetime-local" value={until} onChange={e => setUntil(e.target.value)} /></label>
    <label>Motivo <input value={reason} maxLength={500} onChange={e => setReason(e.target.value)} /></label>
    <button type="button" disabled={!valid || busy} onClick={() => void submit()}>Confirmar adiamento</button>{" "}
    <button type="button" disabled={busy} onClick={() => { setOpen(false); setMessage(""); intent.current = null; }}>Cancelar</button>
    {message && <p role="alert">{message}</p>}
  </section>;
}
