import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { deleteHours, evaluateHours, getHours, listHoursInboxes, listHoursSectors, putHours, type HoursConfiguration, type HoursDecision } from "@/api/businesshours";
import { PanelRequestError } from "@/api/problems";
import { denyHoursScope, forgetHoursQueries, hoursKey, hoursScopeToken, subscribeHoursDenial } from "@/features/businesshours/cache";
import { configError, dayLabel, type HoursInterval } from "@/features/businesshours/schedule";

type RowInterval = HoursInterval & { rowKey: string };

function rowIntervals(intervals: readonly HoursInterval[]): RowInterval[] {
  return intervals.map((item) => ({ day: item.day, start: item.start, end: item.end, rowKey: crypto.randomUUID() }));
}

function bareIntervals(rows: readonly RowInterval[]): HoursInterval[] {
  return rows.map(({ day, start, end }) => ({ day, start, end }));
}
import type { PanelPrincipal } from "@/stores/session-store";

function httpStatus(error: unknown): number | null {
  if (typeof error !== "object" || error === null || !("status" in error)) return null;
  const status = error.status;
  return typeof status === "number" ? status : null;
}

function denied(error: unknown): boolean {
  return [403, 404].includes(httpStatus(error) ?? 0);
}

function denialMessage(error: unknown): string {
  if (error instanceof PanelRequestError && error.message.trim().length > 0) return error.message;
  if (typeof error === "object" && error !== null && "message" in error && typeof error.message === "string" && error.message.trim().length > 0 && httpStatus(error) !== null) {
    return error.message;
  }
  return "Acesso negado.";
}

function invalidated(): DOMException {
  return new DOMException("A consulta foi invalidada.", "AbortError");
}

function textId(value: unknown): string {
  return typeof value === "string" ? value : "";
}

type HoursLifetime = {
  closed: { current: boolean };
  revoked: { current: boolean };
};

function alive(scope: HoursLifetime): boolean {
  return !scope.closed.current && !scope.revoked.current;
}

async function readScoped<T>(
  scope: HoursLifetime,
  signal: AbortSignal | undefined,
  read: () => Promise<T>,
  onDenied: (error: unknown) => void,
): Promise<T> {
  if (!alive(scope)) throw invalidated();
  try {
    const data = await read();
    if (!alive(scope) || signal?.aborted) throw invalidated();
    return data;
  } catch (error) {
    if (denied(error)) onDenied(error);
    throw error;
  }
}

export function HoursSettings({ principal }: { principal: PanelPrincipal }) {
  const [open, setOpen] = useState(false);
  if (!open) {
    return <button type="button" onClick={() => setOpen(true)}>Mostrar horário e plantão</button>;
  }
  return <HoursBody key={hoursScopeToken(principal)} principal={principal} />;
}

function HoursBody({ principal }: { principal: PanelPrincipal }) {
  const client = useQueryClient();
  const token = hoursScopeToken(principal);
  const closed = useRef(false);
  const revokedRef = useRef(false);
  const scope: HoursLifetime = { closed, revoked: revokedRef };
  const [revoked, setRevoked] = useState(false);
  const [denial, setDenial] = useState("");
  const [inboxId, setInboxId] = useState("");
  function onDenied(error: unknown) {
    denyHoursScope(client, principal, denialMessage(error));
  }
  const onDeniedRef = useRef(onDenied);
  useLayoutEffect(() => {
    onDeniedRef.current = onDenied;
  });
  const inboxes = useQuery({
    queryKey: hoursKey(principal, "inboxes"),
    queryFn: ({ signal }) => readScoped(scope, signal, () => listHoursInboxes(signal), onDenied),
    enabled: !revoked,
    retry: false,
  });
  const sectors = useQuery({
    queryKey: hoursKey(principal, "sectors"),
    queryFn: ({ signal }) => readScoped(scope, signal, () => listHoursSectors(signal), onDenied),
    enabled: !revoked,
    retry: false,
  });
  useEffect(() => {
    closed.current = false;
    return () => {
      closed.current = true;
      forgetHoursQueries(client, principal);
    };
  }, [client, principal]);
  useLayoutEffect(() => subscribeHoursDenial((deniedToken, message) => {
    if (deniedToken !== token) return;
    revokedRef.current = true;
    setRevoked(true);
    setDenial(message);
  }), [token]);
  useLayoutEffect(() => {
    const error = denied(inboxes.error) ? inboxes.error : sectors.error;
    if (denied(error)) onDeniedRef.current(error);
  }, [inboxes.error, sectors.error]);
  const writable = principal.role === "admin";
  if (revoked) {
    return (
      <section aria-label="Horário de atendimento">
        <h2>Horário e plantão</h2>
        <p>{denial}</p>
      </section>
    );
  }
  return (
    <section aria-label="Horário de atendimento">
      <h2>Horário e plantão</h2>
      <p>A configuração é por caixa. Lista vazia significa fechado, não expediente de 24 horas. Nenhum envio automático parte desta tela.</p>
      {principal.role === "supervisor" ? <p>Supervisor consulta a configuração e a decisão. Gravar cabe ao administrador.</p> : null}
      {inboxes.isPending ? <p>Carregando caixas…</p> : null}
      {inboxes.isError ? <p>Não foi possível carregar as caixas.</p> : null}
      <label>
        Caixa do horário
        <select value={inboxId} onChange={(event) => setInboxId(event.target.value)}>
          <option value="">Selecione uma caixa</option>
          {(inboxes.data?.inboxes ?? []).map((inbox) => (
            <option key={inbox.id} value={inbox.id}>{inbox.name}</option>
          ))}
        </select>
      </label>
      {inboxId ? (
        <HoursEditor
          key={inboxId}
          principal={principal}
          inboxId={inboxId}
          writable={writable}
          sectors={(sectors.data?.sectors ?? []).filter((sector) => sector.active).map((sector) => ({ id: sector.id, name: sector.name }))}
          parent={scope}
          onDenied={onDenied}
        />
      ) : null}
    </section>
  );
}

function HoursEditor({
  principal,
  inboxId,
  writable,
  sectors,
  parent,
  onDenied,
}: {
  principal: PanelPrincipal;
  inboxId: string;
  writable: boolean;
  sectors: readonly { id: string; name: string }[];
  parent: HoursLifetime;
  onDenied: (error: unknown) => void;
}) {
  const client = useQueryClient();
  const token = hoursScopeToken(principal);
  const closed = useRef(false);
  const revokedRef = useRef(false);
  const scope: HoursLifetime = { closed, revoked: revokedRef };
  const key = hoursKey(principal, inboxId, "configuration");
  const onDeniedRef = useRef(onDenied);
  useLayoutEffect(() => {
    onDeniedRef.current = onDenied;
  });
  const stillCurrent = () => alive(parent) && alive(scope);
  const query = useQuery({
    queryKey: key,
    queryFn: async ({ signal }) => {
      if (!stillCurrent()) throw invalidated();
      try {
        const data = await getHours(inboxId, signal);
        if (!stillCurrent() || signal.aborted) throw invalidated();
        return data;
      } catch (error) {
        if (denied(error)) onDenied(error);
        throw error;
      }
    },
    retry: false,
  });
  useEffect(() => {
    closed.current = false;
    return () => {
      closed.current = true;
    };
  }, []);
  useLayoutEffect(() => subscribeHoursDenial((deniedToken) => {
    if (deniedToken !== token) return;
    revokedRef.current = true;
  }), [token]);
  useLayoutEffect(() => {
    if (denied(query.error)) onDeniedRef.current(query.error);
  }, [query.error]);
  if (query.isPending) return <p>Carregando horário…</p>;
  if (query.isError) return <p>Não foi possível carregar o horário.</p>;
  return (
    <HoursForm
      key={`${query.data.version}:${query.data.configured}`}
      inboxId={inboxId}
      initial={query.data}
      writable={writable}
      sectors={sectors}
      stillCurrent={stillCurrent}
      onDenied={onDenied}
      onSaved={(next) => {
        if (!stillCurrent()) return;
        client.setQueryData(key, next);
      }}
    />
  );
}

function HoursForm({
  inboxId,
  initial,
  writable,
  sectors,
  stillCurrent,
  onDenied,
  onSaved,
}: {
  inboxId: string;
  initial: HoursConfiguration;
  writable: boolean;
  sectors: readonly { id: string; name: string }[];
  stillCurrent: () => boolean;
  onDenied: (error: unknown) => void;
  onSaved: (next: HoursConfiguration) => void;
}) {
  const [intervals, setIntervals] = useState<RowInterval[]>(() => rowIntervals(initial.config.intervals ?? []));
  const [welcome, setWelcome] = useState(initial.config.welcome ?? "");
  const [outside, setOutside] = useState(initial.config.outside_hours ?? "");
  const [nobody, setNobody] = useState(initial.config.nobody_available ?? "");
  const [onCall, setOnCall] = useState(textId(initial.config.on_call_sector_id));
  const [version, setVersion] = useState(initial.version);
  const [instant, setInstant] = useState("");
  const [decision, setDecision] = useState<HoursDecision | null>(null);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const pending = useRef<{ id: string; hash: string } | null>(null);

  function payloadHash(): string {
    return JSON.stringify({ intervals: bareIntervals(intervals), welcome, outside, nobody, onCall, version });
  }

  function apply(next: HoursConfiguration) {
    if (!stillCurrent()) return;
    setVersion(next.version);
    onSaved(next);
    pending.current = null;
  }

  async function save() {
    const savedIntervals = bareIntervals(intervals);
    const error = configError({ intervals: savedIntervals, welcome, outsideHours: outside, nobodyAvailable: nobody });
    if (error) {
      setMessage(error);
      return;
    }
    const hash = payloadHash();
    if (!pending.current || pending.current.hash !== hash) pending.current = { id: crypto.randomUUID(), hash };
    const request = pending.current;
    if (!request || !stillCurrent()) return;
    setBusy(true);
    setMessage("");
    try {
      const next = await putHours(inboxId, {
        request_id: request.id,
        expected_version: version,
        config: {
          intervals: savedIntervals,
          welcome,
          outside_hours: outside,
          nobody_available: nobody,
          on_call_sector_id: onCall || null,
        },
      } as Parameters<typeof putHours>[1]);
      if (!stillCurrent()) return;
      apply(next);
      setMessage("Horário gravado.");
    } catch (error) {
      if (denied(error)) {
        onDenied(error);
        return;
      }
      if (!stillCurrent()) return;
      setMessage(error instanceof PanelRequestError ? error.message : "Não foi possível gravar o horário.");
    } finally {
      if (stillCurrent()) setBusy(false);
    }
  }

  async function remove() {
    const hash = `delete:${version}`;
    if (!pending.current || pending.current.hash !== hash) pending.current = { id: crypto.randomUUID(), hash };
    const request = pending.current;
    if (!request || !stillCurrent()) return;
    setBusy(true);
    setMessage("");
    try {
      const next = await deleteHours(inboxId, { request_id: request.id, expected_version: version });
      if (!stillCurrent()) return;
      setIntervals([]);
      setWelcome("");
      setOutside("");
      setNobody("");
      setOnCall("");
      apply(next);
      setMessage("Configuração retirada. O histórico de versão permanece.");
    } catch (error) {
      if (denied(error)) {
        onDenied(error);
        return;
      }
      if (!stillCurrent()) return;
      setMessage(error instanceof PanelRequestError ? error.message : "Não foi possível retirar a configuração.");
    } finally {
      if (stillCurrent()) setBusy(false);
    }
  }

  async function evaluate() {
    const parsed = instant ? new Date(instant) : null;
    if (!parsed || Number.isNaN(parsed.getTime())) {
      setMessage("Informe o instante que o servidor deve avaliar.");
      return;
    }
    if (!stillCurrent()) return;
    setBusy(true);
    setMessage("");
    try {
      const result = await evaluateHours(inboxId, parsed.toISOString());
      if (!stillCurrent()) return;
      setDecision(result);
    } catch (error) {
      if (denied(error)) {
        onDenied(error);
        return;
      }
      if (!stillCurrent()) return;
      setDecision(null);
      setMessage(error instanceof PanelRequestError ? error.message : "Não foi possível consultar a decisão.");
    } finally {
      if (stillCurrent()) setBusy(false);
    }
  }

  return (
    <div>
      <p>Fuso do provedor: {initial.timezone || "indisponível"}. Versão {version}. {initial.configured ? "Há configuração ativa." : "Não há configuração ativa."}</p>
      {intervals.length === 0 ? <p>Sem intervalos a caixa fica fechada. Isso não significa 24 horas.</p> : null}
      <ul>
        {intervals.map((item, index) => (
          <li key={item.rowKey}>
            <label>
              Dia
              <select disabled={!writable} value={item.day} onChange={(event) => setIntervals((current) => current.map((row, rowIndex) => rowIndex === index ? { ...row, day: Number(event.target.value) } : row))}>
                {[0, 1, 2, 3, 4, 5, 6].map((day) => <option key={day} value={day}>{dayLabel(day)}</option>)}
              </select>
            </label>
            <label>Início <input disabled={!writable} type="number" min={0} max={1439} value={item.start} onChange={(event) => setIntervals((current) => current.map((row, rowIndex) => rowIndex === index ? { ...row, start: Number(event.target.value) } : row))} /></label>
            <label>Fim <input disabled={!writable} type="number" min={1} max={1440} value={item.end} onChange={(event) => setIntervals((current) => current.map((row, rowIndex) => rowIndex === index ? { ...row, end: Number(event.target.value) } : row))} /></label>
            {writable ? <button type="button" onClick={() => setIntervals((current) => current.filter((_, rowIndex) => rowIndex !== index))}>Remover intervalo</button> : null}
          </li>
        ))}
      </ul>
      {writable ? <button type="button" onClick={() => setIntervals((current) => [...current, { rowKey: crypto.randomUUID(), day: 1, start: 540, end: 720 }])}>Adicionar intervalo</button> : null}
      <label>Boas-vindas <textarea disabled={!writable} value={welcome} onChange={(event) => setWelcome(event.target.value)} /></label>
      <label>Fora do expediente <textarea disabled={!writable} value={outside} onChange={(event) => setOutside(event.target.value)} /></label>
      <label>Ninguém disponível <textarea disabled={!writable} value={nobody} onChange={(event) => setNobody(event.target.value)} /></label>
      <label>
        Plantão
        <select disabled={!writable} value={onCall} onChange={(event) => setOnCall(event.target.value)}>
          <option value="">Sem plantão</option>
          {sectors.map((sector) => <option key={sector.id} value={sector.id}>{sector.name}</option>)}
        </select>
      </label>
      {writable ? <button type="button" disabled={busy} onClick={() => void save()}>Substituir horário</button> : null}
      {writable ? <button type="button" disabled={busy} onClick={() => void remove()}>Retirar configuração</button> : null}
      <label>
        Instante avaliado
        <input type="datetime-local" value={instant} onChange={(event) => setInstant(event.target.value)} />
      </label>
      <button type="button" disabled={busy} onClick={() => void evaluate()}>Consultar decisão</button>
      {decision ? <DecisionView decision={decision} /> : null}
      {message ? <p>{message}</p> : null}
    </div>
  );
}

function DecisionView({ decision }: { decision: HoursDecision }) {
  return (
    <div>
      <p>{decision.configured ? "Configuração presente." : "Sem configuração ativa."} {decision.open ? "Dentro do expediente." : "Fora do expediente."}</p>
      <p>Instante do servidor: {decision.instant}</p>
      <p>Próxima abertura: {decision.next_opening ?? "nenhuma nesta busca"}</p>
      <p>Destino: {textId(decision.destination_sector_id) || "nenhum autorizado"}</p>
      <p>Plantão autorizado: {textId(decision.on_call_sector_id) || "nenhum"}</p>
    </div>
  );
}
