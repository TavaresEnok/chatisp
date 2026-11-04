import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { listHoursInboxes, listHoursSectors } from "@/api/businesshours";
import * as automation from "@/api/automation";
import type { TriageOption } from "@/api/automation";
import { PanelRequestError } from "@/api/problems";
import { BotHookNoticePanel } from "@/features/bothook/bot-hook-notice";
import { presenceScope } from "@/features/presence/operator-presence";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";

const conflict = "Outra pessoa alterou esta configuração. Os valores atuais foram recarregados.";
function failure(error: unknown, fallback: string) {
  return error instanceof PanelRequestError && error.status === 409 ? conflict : fallback;
}
/** True while the session that started an operation is still the current one. */
function sameScope(principal: PanelPrincipal) {
  const current = useSessionStore.getState().principal;
  return !!current && presenceScope(current) === presenceScope(principal);
}

/**
 * Automation of attendance: tenant delays (inactivity, presence tolerance) and,
 * per inbox, the reopen window X and the triage menu. Admin edits; supervisor
 * reads. The server remains the authority for every value and permission.
 */
export function AutomationSettings({ principal }: { principal: PanelPrincipal }) {
  const [open, setOpen] = useState(false);
  if (!open) return <button type="button" onClick={() => setOpen(true)}>Mostrar automação do atendimento</button>;
  return <AutomationBody key={presenceScope(principal)} principal={principal} />;
}

function AutomationBody({ principal }: { principal: PanelPrincipal }) {
  const scope = presenceScope(principal);
  const [inbox, setInbox] = useState("");
  const boxes = useQuery({ queryKey: ["automation", scope, "inboxes"], queryFn: ({ signal }) => listHoursInboxes(signal), retry: false });
  return <section aria-label="Automação do atendimento" className="space-y-4">
    <h2>Automação do atendimento</h2>
    <InactivityForm principal={principal} />
    <PresenceToleranceForm principal={principal} />
    <label>Caixa <select value={inbox} onChange={e => setInbox(e.target.value)}><option value="">Selecione</option>{boxes.data?.inboxes?.map(b => <option key={b.id} value={b.id}>{b.name}</option>)}</select></label>
    {boxes.isError && <p role="alert">Não foi possível listar as caixas.</p>}
    {inbox && <BotHookNoticePanel key={`${scope}:hook:${inbox}`} inbox={inbox} />}
    {inbox && <ReopenWindowForm key={`${scope}:reopen:${inbox}`} principal={principal} inbox={inbox} />}
    {inbox && <TriageMenuForm key={`${scope}:menu:${inbox}`} principal={principal} inbox={inbox} />}
  </section>;
}

function InactivityForm({ principal }: { principal: PanelPrincipal }) {
  const admin = principal.role === "admin";
  const [value, setValue] = useState<automation.InactivitySettings | null>(null);
  const [warning, setWarning] = useState(15);
  const [close, setClose] = useState(15);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    automation.getInactivity(controller.signal).then(next => {
      setValue(next); setWarning(Math.round((next.warning_after_seconds ?? 900) / 60)); setClose(Math.round((next.close_after_seconds ?? 900) / 60));
    }).catch(() => { if (!controller.signal.aborted) setMessage("Não foi possível consultar os prazos de inatividade."); });
    return () => controller.abort();
  }, [reload]);
  const valid = Number.isInteger(warning) && Number.isInteger(close) && warning >= 1 && close >= 1 && warning <= 10080 && close <= 10080;
  async function save() {
    if (!value || !valid) return;
    setBusy(true); setMessage("");
    try {
      const next = await automation.putInactivity({ expected_version: value.version, warning_after_seconds: warning * 60, close_after_seconds: close * 60 });
      if (sameScope(principal)) { setValue(next); setMessage("Prazos de inatividade salvos."); }
    } catch (error) { if (sameScope(principal)) { setMessage(failure(error, "Não foi possível salvar os prazos.")); setReload(n => n + 1); } }
    finally { setBusy(false); }
  }
  return <fieldset aria-label="Encerramento por inatividade">
    <legend>Encerramento por inatividade</legend>
    <p className="text-sm text-muted-foreground">Vale para conversas aguardando o cliente depois de uma resposta humana aceita. O aviso precisa ser aceito pelo canal antes de armar o encerramento.{value && !value.configured ? " Usando o padrão (15 + 15 minutos)." : ""}</p>
    <label>Aviso após (minutos) <input type="number" min={1} max={10080} value={warning} disabled={!admin || !value} onChange={e => setWarning(Number(e.target.value))} /></label>
    <label>Encerrar após o aviso (minutos) <input type="number" min={1} max={10080} value={close} disabled={!admin || !value} onChange={e => setClose(Number(e.target.value))} /></label>
    {admin && <button type="button" disabled={!value || !valid || busy} onClick={() => void save()}>Salvar prazos</button>}
    {message && <p role="status">{message}</p>}
  </fieldset>;
}

function PresenceToleranceForm({ principal }: { principal: PanelPrincipal }) {
  const admin = principal.role === "admin";
  const [value, setValue] = useState<automation.PresenceSettings | null>(null);
  const [minutes, setMinutes] = useState(5);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    automation.getPresenceSettings(controller.signal).then(next => { setValue(next); setMinutes(Math.max(1, Math.round((next.offline_tolerance_seconds ?? 300) / 60))); })
      .catch(() => { if (!controller.signal.aborted) setMessage("Não foi possível consultar a tolerância de presença."); });
    return () => controller.abort();
  }, [reload]);
  const valid = Number.isInteger(minutes) && minutes >= 1 && minutes <= 1440;
  async function save() {
    if (!value || !valid) return;
    setBusy(true); setMessage("");
    try {
      const next = await automation.putPresenceSettings({ expected_version: value.version, offline_tolerance_seconds: minutes * 60 });
      if (sameScope(principal)) { setValue(next); setMessage("Tolerância de presença salva."); }
    } catch (error) { if (sameScope(principal)) { setMessage(failure(error, "Não foi possível salvar a tolerância.")); setReload(n => n + 1); } }
    finally { setBusy(false); }
  }
  return <fieldset aria-label="Tolerância offline">
    <legend>Tolerância offline</legend>
    <p className="text-sm text-muted-foreground">Tempo sem nenhuma aba conectada antes de o atendente deixar de receber atribuições automáticas.</p>
    <label>Tolerância (minutos) <input type="number" min={1} max={1440} value={minutes} disabled={!admin || !value} onChange={e => setMinutes(Number(e.target.value))} /></label>
    {admin && <button type="button" disabled={!value || !valid || busy} onClick={() => void save()}>Salvar tolerância</button>}
    {message && <p role="status">{message}</p>}
  </fieldset>;
}

function ReopenWindowForm({ principal, inbox }: { principal: PanelPrincipal; inbox: string }) {
  const admin = principal.role === "admin";
  const [hours, setHours] = useState<number | null>(null);
  const [draft, setDraft] = useState(0);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const controller = new AbortController();
    automation.getReopenWindow(inbox, controller.signal).then(next => { setHours(next.hours); setDraft(next.hours); })
      .catch(() => { if (!controller.signal.aborted) setMessage("Não foi possível consultar a janela de reabertura."); });
    return () => controller.abort();
  }, [inbox]);
  const valid = Number.isInteger(draft) && draft >= 0 && draft <= 720;
  async function save() {
    if (!valid) return;
    setBusy(true); setMessage("");
    try {
      const next = await automation.setReopenWindow(inbox, draft);
      if (sameScope(principal)) { setHours(next.hours); setMessage("Janela de reabertura salva."); }
    } catch { if (sameScope(principal)) setMessage("Não foi possível salvar a janela de reabertura."); }
    finally { setBusy(false); }
  }
  return <fieldset aria-label="Reabertura automática">
    <legend>Reabertura automática</legend>
    <p className="text-sm text-muted-foreground">Nova mensagem do cliente até X horas depois do encerramento reabre o mesmo atendimento, com o mesmo protocolo e um novo ciclo. Depois disso, ou com 0, abre um novo atendimento e protocolo.</p>
    <label>Janela X (horas) <input type="number" min={0} max={720} value={draft} disabled={!admin || hours === null} onChange={e => setDraft(Number(e.target.value))} /></label>
    {hours !== null && <p>{hours === 0 ? "Reabertura automática desligada." : `Reabre até ${hours} h após o encerramento.`}</p>}
    {admin && <button type="button" disabled={hours === null || !valid || busy || draft === hours} onClick={() => void save()}>Salvar janela</button>}
    {message && <p role="status">{message}</p>}
  </fieldset>;
}

type OptionDraft = { key: string; label: string; destination: "human" | "sector"; sector_id: string; reason: string };
function toDraft(option: TriageOption): OptionDraft {
  return { key: option.key, label: option.label, destination: option.destination === "sector" ? "sector" : "human", sector_id: option.sector_id ?? "", reason: option.reason ?? "" };
}
function fromDraft(option: OptionDraft): TriageOption {
  const key = option.key.trim(), label = option.label.trim();
  return option.destination === "human"
    ? { key, label, destination: "human", reason: "" }
    : { key, label, destination: "sector", sector_id: option.sector_id, reason: option.reason.trim() };
}
const defaultOptions: OptionDraft[] = [{ key: "1", label: "Falar com um atendente", destination: "human", sector_id: "", reason: "" }];

function TriageMenuForm({ principal, inbox }: { principal: PanelPrincipal; inbox: string }) {
  const admin = principal.role === "admin";
  const scope = presenceScope(principal);
  const sectors = useQuery({ queryKey: ["automation", scope, "sectors"], queryFn: ({ signal }) => listHoursSectors(signal), retry: false });
  const [menu, setMenu] = useState<automation.TriageMenu | null>(null);
  const [attempts, setAttempts] = useState(3);
  const [options, setOptions] = useState<OptionDraft[]>(defaultOptions);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    automation.getTriageMenu(inbox, controller.signal).then(next => {
      setMenu(next); setAttempts(next.max_attempts || 3);
      setOptions(next.configured && next.options?.length ? next.options.map(toDraft) : defaultOptions);
    }).catch(() => { if (!controller.signal.aborted) setMessage("Não foi possível consultar o menu de triagem."); });
    return () => controller.abort();
  }, [inbox, reload]);
  const hasHuman = options.some(o => o.destination === "human");
  const complete = options.every(o => /^[0-9]{1,4}$/.test(o.key.trim()) && o.label.trim() && (o.destination === "human" || o.sector_id));
  const valid = hasHuman && complete && options.length >= 1 && options.length <= 12 && attempts >= 1 && attempts <= 5;
  function update(index: number, patch: Partial<OptionDraft>) { setOptions(list => list.map((o, i) => i === index ? { ...o, ...patch } : o)); }
  async function save() {
    if (!menu || !valid) return;
    setBusy(true); setMessage("");
    try {
      const next = await automation.putTriageMenu(inbox, { expected_version: menu.version, max_attempts: attempts, options: options.map(fromDraft) });
      if (sameScope(principal)) { setMenu(next); setMessage("Menu de triagem salvo."); }
    } catch (error) {
      if (!sameScope(principal)) return;
      if (error instanceof PanelRequestError && error.status === 409) { setMessage(conflict); setReload(n => n + 1); }
      else setMessage("O menu não foi aceito. Confira chaves e rótulos únicos, um setor por opção de setor e a opção de atendente.");
    } finally { setBusy(false); }
  }
  const activeSectors = sectors.data?.sectors?.filter(s => s.active) ?? [];
  return <fieldset aria-label="Menu de triagem">
    <legend>Menu de triagem</legend>
    <p className="text-sm text-muted-foreground">Opções numeradas enviadas ao cliente na chegada. A opção de atendente é obrigatória. O menu só é enviado nas caixas habilitadas na configuração do serviço (CHATISP_TRIAGE_INBOXES).{menu && !menu.configured ? " Esta caixa ainda não tem menu salvo." : ""}</p>
    <label>Tentativas antes do setor padrão <input type="number" min={1} max={5} value={attempts} disabled={!admin || !menu} onChange={e => setAttempts(Number(e.target.value))} /></label>
    <ol className="space-y-2">
      {options.map((option, index) => <li key={index} aria-label={`Opção ${index + 1}`}>
        <label>Chave <input value={option.key} maxLength={4} size={4} inputMode="numeric" pattern="[0-9]{1,4}" disabled={!admin} onChange={e => update(index, { key: e.target.value })} /></label>{" "}
        <label>Rótulo <input value={option.label} maxLength={80} disabled={!admin} onChange={e => update(index, { label: e.target.value })} /></label>{" "}
        <label>Destino <select value={option.destination} disabled={!admin} onChange={e => update(index, { destination: e.target.value as OptionDraft["destination"], sector_id: "", reason: "" })}><option value="human">Atendente</option><option value="sector">Setor</option></select></label>{" "}
        {option.destination === "sector" && <>
          <label>Setor <select value={option.sector_id} disabled={!admin} onChange={e => update(index, { sector_id: e.target.value })}><option value="">Selecione</option>{activeSectors.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}</select></label>{" "}
          <label>Motivo <input value={option.reason} maxLength={40} disabled={!admin} onChange={e => update(index, { reason: e.target.value })} /></label>{" "}
        </>}
        {admin && <button type="button" disabled={options.length <= 1} onClick={() => setOptions(list => list.filter((_, i) => i !== index))}>Remover opção {index + 1}</button>}
      </li>)}
    </ol>
    {admin && <button type="button" disabled={options.length >= 12} onClick={() => setOptions(list => [...list, { key: String(list.length + 1), label: "", destination: "sector", sector_id: "", reason: "" }])}>Adicionar opção</button>}{" "}
    {!hasHuman && <p role="alert">Inclua uma opção para falar com atendente.</p>}
    {admin && <button type="button" disabled={!menu || !valid || busy} onClick={() => void save()}>Salvar menu</button>}
    {sectors.isError && <p role="alert">Não foi possível listar os setores.</p>}
    {message && <p role="status">{message}</p>}
  </fieldset>;
}
