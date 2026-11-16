import { Download } from "lucide-react";
import { useState } from "react";
import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { getReport, listReportInboxes, listReportSectors, reportDirectoryKey, reportQueryKey, type AttendanceReport, type ReportOption, type ReportQuery, type ReportStats } from "@/api/reports";
import { PanelRequestError } from "@/api/problems";
import { Heatmap } from "@/features/reports/heatmap";
import type { PanelPrincipal } from "@/stores/session-store";

function civil(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

function initialRange() {
  const end = new Date();
  const start = new Date(end);
  start.setDate(end.getDate() - 6);
  return { from: civil(start), to: civil(end) };
}

export function formatDuration(seconds: number | null | undefined): string {
  if (seconds == null) return "não aplicável";
  const whole = Math.max(0, Math.round(seconds));
  const hours = Math.floor(whole / 3600);
  const minutes = Math.round((whole % 3600) / 60);
  if (hours > 0 && minutes > 0) return `${hours} h ${minutes} min`;
  if (hours > 0) return `${hours} h`;
  if (whole >= 60) return `${minutes} min`;
  return `${whole} s`;
}

function metric(title: string, stats: ReportStats, empty: string) {
  const value = stats.sample === 0
    ? (stats.not_applicable > 0 ? "não aplicável" : empty)
    : `${formatDuration(stats.average_seconds)} (mediana ${formatDuration(stats.median_seconds)}, amostra ${stats.sample}${stats.not_applicable ? `, fora da amostra ${stats.not_applicable}` : ""})`;
  return <p className="sr-only">{title}: {value}</p>;
}

const numberFormat = new Intl.NumberFormat("pt-BR");

/** Linha da tabela de tempos: a mesma leitura da frase por extenso, em colunas. */
function TimeRow({ title, stats, empty }: { title: string; stats: ReportStats; empty: string }) {
  return <div className="report-times-row">
    <span>{title}</span>
    {stats.sample === 0
      ? <span className="report-times-empty">{stats.not_applicable > 0 ? "não aplicável" : empty}</span>
      : <><b>{formatDuration(stats.average_seconds)}</b><b>{formatDuration(stats.median_seconds)}</b><span>{numberFormat.format(stats.sample)}</span></>}
  </div>;
}

type BarRow = { key: string; label: string; value: number; detail?: string };

/** Barras proporcionais ao maior valor da lista. É só reforço visual: os números por extenso ficam na lista ao lado. */
function Bars({ rows, unit }: { rows: BarRow[]; unit?: string }) {
  const max = Math.max(1, ...rows.map((row) => row.value));
  return <div className="report-bars" aria-hidden="true">
    {rows.map((row) => <div key={row.key} className="report-bar" title={`${row.label}: ${numberFormat.format(row.value)}${unit ? ` ${unit}` : ""}`}>
      <span className="report-bar-label">{row.label}</span>
      <b>{numberFormat.format(row.value)}</b>
      <span className="report-bar-track"><span style={{ width: `${Math.max(0, Math.min(100, (row.value / max) * 100))}%` }} /></span>
      {row.detail ? <small>{row.detail}</small> : null}
    </div>)}
  </div>;
}

/** Ciclos por agrupamento (dia, semana ou mês), uma barra por período. */
function BucketChart({ buckets }: { buckets: AttendanceReport["buckets"] }) {
  const max = Math.max(1, ...buckets.map((row) => row.cycles));
  const first = buckets[0];
  const last = buckets[buckets.length - 1];
  return <div className="report-chart" aria-hidden="true">
    <div className="report-chart-plot">
      {buckets.map((row) => <div key={row.key} className="report-chart-column" title={`${row.key}: ${numberFormat.format(row.cycles)} ciclos, ${numberFormat.format(row.messages)} mensagens, ${numberFormat.format(row.closed)} encerrados`}>
        <span className="report-chart-value">{numberFormat.format(row.cycles)}</span>
        <span className="report-chart-bar" style={{ height: `${Math.max(2, (row.cycles / max) * 100)}%` }} />
      </div>)}
    </div>
    <div className="report-chart-axis"><span>{first?.key}</span>{last && last !== first ? <span>{last.key}</span> : null}</div>
  </div>;
}

function LiveBoard({ report }: { report: AttendanceReport }) {
  const live = report.live;
  return <section aria-label="Situação agora" className="live-board">
    <div className="section-heading"><h2>Visão do atendimento</h2><span>Agora</span></div>
    <ul className="live-metrics">
      {([
        ["Na fila", live.in_queue], ["Em atendimento", live.in_attendance],
        ["Aguardando o cliente", live.waiting_customer], ["Adiadas", live.snoozed],
        ["Sem responsável", live.unassigned], ["Atendentes online", live.online_attendants],
        ["Maior espera", formatDuration(live.longest_queue_wait_seconds)], ["Alertas de espera", live.wait_alerts],
      ] as const).map(([label, value]) => <li key={label}><span className="sr-only">{label}: {value}</span><span aria-hidden="true">{label}</span><strong aria-hidden="true">{value}</strong></li>)}
    </ul>
  </section>;
}

function download(csv: string) {
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = "relatorio-atendimento.csv";
  link.click();
  URL.revokeObjectURL(url);
}

function principalScope(principal: PanelPrincipal): string {
  return [principal.tenantId, principal.sessionId, principal.userId, principal.membershipId, principal.role].join(":");
}

function chosenId(selected: string, options: ReportOption[], known: boolean, denied: boolean): string {
  if (selected === "" || denied) return "";
  if (!known) return selected;
  return options.some((option) => option.id === selected) ? selected : "";
}

type ReportScopeCache = { generation: number; denial: string };

const reportScopeCaches = new WeakMap<QueryClient, Map<string, ReportScopeCache>>();

function reportScopeCache(client: QueryClient, scope: string): ReportScopeCache {
  let scopes = reportScopeCaches.get(client);
  if (!scopes) {
    scopes = new Map();
    reportScopeCaches.set(client, scopes);
  }
  const existing = scopes.get(scope);
  if (existing) return existing;
  const created = { generation: 0, denial: "false:false" };
  scopes.set(scope, created);
  return created;
}

function DirectoryNotice({ kind, pending, denied, failed, empty }: { kind: "setores" | "caixas"; pending: boolean; denied: boolean; failed: boolean; empty: boolean }) {
  if (pending) return <p role="status">Carregando {kind}…</p>;
  if (denied) return <p role="alert">Acesso negado à lista de {kind}.</p>;
  if (failed) return <p role="alert">Não foi possível carregar a lista de {kind}.</p>;
  if (empty) return <p>{kind === "setores" ? "Nenhum setor autorizado nesta sessão." : "Nenhuma caixa autorizada nesta sessão."}</p>;
  return null;
}

export function AttendanceReportView({ principal, mode }: { principal: PanelPrincipal; mode: "live" | "full" }) {
  const range = initialRange();
  const scope = principalScope(principal);
  const client = useQueryClient();
  const [from, setFrom] = useState(range.from);
  const [to, setTo] = useState(range.to);
  const [group, setGroup] = useState<"day" | "week" | "month">("day");
  const [picked, setPicked] = useState({ scope, sectorId: "", inboxId: "" });
  const sectorId = picked.scope === scope ? picked.sectorId : "";
  const inboxId = picked.scope === scope ? picked.inboxId : "";
  const sectorsQuery = useQuery({
    queryKey: reportDirectoryKey("sectors", principal),
    queryFn: ({ signal }) => listReportSectors(signal),
    enabled: mode === "full",
    retry: false,
  });
  const inboxesQuery = useQuery({
    queryKey: reportDirectoryKey("inboxes", principal),
    queryFn: ({ signal }) => listReportInboxes(signal),
    enabled: mode === "full",
    retry: false,
  });
  const sectorsDenied = sectorsQuery.isError && sectorsQuery.error instanceof PanelRequestError && sectorsQuery.error.status === 403;
  const inboxesDenied = inboxesQuery.isError && inboxesQuery.error instanceof PanelRequestError && inboxesQuery.error.status === 403;
  const directoryDenied = mode === "full" && (sectorsDenied || inboxesDenied);
  if (mode === "full") {
    const denial = `${sectorsDenied}:${inboxesDenied}`;
    const cache = reportScopeCache(client, scope);
    if (cache.denial !== denial) {
      cache.denial = denial;
      if (sectorsDenied || inboxesDenied) {
        cache.generation += 1;
        setPicked((current) => ({
          scope,
          sectorId: sectorsDenied ? "" : (current.scope === scope ? current.sectorId : ""),
          inboxId: inboxesDenied ? "" : (current.scope === scope ? current.inboxId : ""),
        }));
      }
    }
  }
  const generation = mode === "full" ? reportScopeCache(client, scope).generation : 0;
  const sectorChoices = sectorsDenied ? [] : (sectorsQuery.data ?? []);
  const inboxChoices = inboxesDenied ? [] : (inboxesQuery.data ?? []);
  const sectorFilter = chosenId(sectorId, sectorChoices, sectorsQuery.data !== undefined && !sectorsDenied, sectorsDenied);
  const inboxFilter = chosenId(inboxId, inboxChoices, inboxesQuery.data !== undefined && !inboxesDenied, inboxesDenied);
  const filters: ReportQuery = mode === "live"
    ? { from: civil(new Date()), to: civil(new Date()), group: "day" }
    : { from, to, group, sectorId: sectorFilter, inboxId: inboxFilter };
  const reportQuery = useQuery({
    queryKey: reportQueryKey(principal, mode, filters, generation),
    queryFn: ({ signal }) => getReport(filters, signal),
    enabled: mode === "live" || !directoryDenied,
    retry: false,
  });
  const reportDenied = reportQuery.isError && reportQuery.error instanceof PanelRequestError && reportQuery.error.status === 403;
  // Cache obsoleto continua com isSuccess enquanto a seleção atual busca de novo.
  // O ao vivo mantém o último quadro validado; o relatório completo não exporta essa resposta.
  const report = directoryDenied || !reportQuery.isSuccess || (mode === "full" && reportQuery.isFetching) ? undefined : reportQuery.data;
  const waitingReport = report === undefined && !directoryDenied && (reportQuery.isPending || reportQuery.isFetching);
  const failedReport = !directoryDenied && reportQuery.isError && !reportQuery.isFetching;
  return <div className="report-view" data-mode={mode}>
    {mode === "full" && <form className="report-filters" onSubmit={(event) => event.preventDefault()}>
      <label>Início <input type="date" value={from} onChange={(event) => setFrom(event.target.value)} /></label>
      <label>Fim <input type="date" value={to} onChange={(event) => setTo(event.target.value)} /></label>
      <label>Agrupamento <select aria-label="Agrupamento" value={group} onChange={(event) => setGroup(event.target.value as "day" | "week" | "month")}><option value="day">Dia</option><option value="week">Semana</option><option value="month">Mês</option></select></label>
      <label>Setor <select aria-label="Setor" value={sectorChoices.some((option) => option.id === sectorId) ? sectorId : ""} onChange={(event) => setPicked({ scope, sectorId: event.target.value, inboxId })}>
        <option value="">Todos os setores autorizados</option>
        {sectorChoices.map((option) => <option key={option.id} value={option.id}>{option.name}</option>)}
      </select></label>
      <label>Caixa <select aria-label="Caixa" value={inboxChoices.some((option) => option.id === inboxId) ? inboxId : ""} onChange={(event) => setPicked({ scope, sectorId, inboxId: event.target.value })}>
        <option value="">Todas as caixas autorizadas</option>
        {inboxChoices.map((option) => <option key={option.id} value={option.id}>{option.name}</option>)}
      </select></label>
      <DirectoryNotice kind="setores" pending={sectorsQuery.isPending} denied={sectorsDenied} failed={sectorsQuery.isError && !sectorsDenied} empty={sectorsQuery.isSuccess && sectorChoices.length === 0} />
      <DirectoryNotice kind="caixas" pending={inboxesQuery.isPending} denied={inboxesDenied} failed={inboxesQuery.isError && !inboxesDenied} empty={inboxesQuery.isSuccess && inboxChoices.length === 0} />
    </form>}
    {waitingReport && <p role="status">Carregando o relatório…</p>}
    {failedReport && <p role="alert">{reportDenied ? "Acesso negado." : "Não foi possível carregar o relatório."}</p>}
    {report !== undefined && <ReportBody report={report} mode={mode} />}
  </div>;
}

function ReportBody({ report, mode }: { report: AttendanceReport; mode: "live" | "full" }) {
  const quiet = report.conversations === 0 && report.cycles === 0 && report.messages === 0;
  return <>
    {mode === "full" && <>
      <section aria-label="Totais do período" className="report-totals">
        <p className="sr-only">Fuso {report.timezone}. Conversas {report.conversations}, ciclos {report.cycles}, mensagens {report.messages}.</p>
        <ul className="live-metrics" aria-hidden="true">
          <li><span>Conversas</span><strong>{numberFormat.format(report.conversations)}</strong></li>
          <li><span>Ciclos</span><strong>{numberFormat.format(report.cycles)}</strong></li>
          <li><span>Mensagens</span><strong>{numberFormat.format(report.messages)}</strong></li>
        </ul>
        <div className="report-totals-foot">
          <p aria-hidden="true">Fuso {report.timezone}</p>
          <button type="button" className="report-download" onClick={() => download(report.csv)}><Download size={15} strokeWidth={1.8} aria-hidden="true" />Baixar CSV</button>
        </div>
        {quiet && <p className="report-quiet">Nenhum ciclo, conversa ou mensagem neste recorte.</p>}
      </section>
    </>}
    <LiveBoard report={report} />
    {mode === "full" && <>
      <div className="report-grid">
        <section aria-label="Tempos" className="report-card">
          <h2>Tempos</h2>
          <p className="report-note">Média, mediana e tamanho da amostra de cada indicador.</p>
          <div className="report-times" aria-hidden="true">
            <div className="report-times-row report-times-head"><span>Indicador</span><span>Média</span><span>Mediana</span><span>Amostra</span></div>
            <TimeRow title="Primeira resposta humana" stats={report.first_response} empty="sem dado no período" />
            <TimeRow title="Tempo até o encerramento" stats={report.close_total} empty="sem encerramento no período" />
            <TimeRow title="Tempo operacional" stats={report.close_operational} empty="sem encerramento no período" />
            <TimeRow title="Espera na fila" stats={report.queue_wait} empty="sem espera encerrada no período" />
          </div>
          {metric("Primeira resposta humana", report.first_response, "sem dado no período")}
          {metric("Tempo até o encerramento", report.close_total, "sem encerramento no período")}
          {metric("Tempo operacional", report.close_operational, "sem encerramento no período")}
          {metric("Espera na fila", report.queue_wait, "sem espera encerrada no período")}
        </section>
        {report.buckets.length > 0 && <section aria-label="Agrupamento" className="report-card">
          <h2>Agrupamento</h2>
          <p className="report-note">Ciclos iniciados em cada período. Passe o ponteiro sobre uma barra para ver os números.</p>
          <BucketChart buckets={report.buckets} />
          <ul className="sr-only">{report.buckets.map((row) => <li key={row.key}>{row.key}: {row.cycles} ciclos, {row.messages} mensagens, {row.closed} encerrados. Primeira resposta humana: {row.first_response_sample === 0 ? "sem amostra" : `${formatDuration(row.first_response_average_seconds)} (amostra ${row.first_response_sample})`}. Tempo operacional do agrupamento: {row.operational_sample === 0 ? "sem amostra" : `${formatDuration(row.operational_average_seconds)} (amostra ${row.operational_sample})`}.</li>)}</ul>
        </section>}
        {report.results.length > 0 && <section aria-label="Resultados" className="report-card"><h2>Resultados</h2>
          <Bars rows={report.results.map((row) => ({ key: `${row.result}-${row.author_kind}`, label: row.label, value: row.count }))} />
          <ul className="sr-only">{report.results.map((row) => <li key={`${row.result}-${row.author_kind}`}>{row.label}: {row.count}</li>)}</ul></section>}
        <section aria-label="Por setor" className="report-card">
          <h2>Por setor</h2>
          {report.sectors.length === 0 ? <p className="report-note">Nenhum setor neste recorte.</p> : <>
            <Bars unit="ciclos" rows={report.sectors.map((row) => ({ key: row.sector_id, label: row.name, value: row.cycles, detail: `${numberFormat.format(row.queue_waits)} esperas · mediana ${formatDuration(row.queue_wait_median_seconds)} · responsabilidade ${formatDuration(row.responsibility_seconds)}` }))} />
            <ul className="sr-only">{report.sectors.map((row) => <li key={row.sector_id}>{row.name}: {row.cycles} ciclos, {row.queue_waits} esperas, mediana {formatDuration(row.queue_wait_median_seconds)}, responsabilidade {formatDuration(row.responsibility_seconds)}</li>)}</ul>
          </>}
        </section>
        <section aria-label="Por atendente" className="report-card">
          <h2>Por atendente</h2>
          {report.attendants.length === 0 ? <p className="report-note">Nenhum atendente neste recorte.</p> : <>
            <Bars unit="mensagens" rows={report.attendants.map((row) => ({ key: row.membership_id, label: row.label, value: row.messages, detail: `${numberFormat.format(row.closures)} ${row.closures === 1 ? "encerramento" : "encerramentos"} · responsabilidade ${formatDuration(row.responsibility_seconds)}` }))} />
            <ul className="sr-only">{report.attendants.map((row) => <li key={row.membership_id}>{row.label}: responsabilidade {formatDuration(row.responsibility_seconds)}, {row.messages} {row.messages === 1 ? "mensagem" : "mensagens"}, {row.closures} {row.closures === 1 ? "encerramento" : "encerramentos"}</li>)}</ul>
          </>}
        </section>
        <section aria-label="Por caixa" className="report-card">
          <h2>Por caixa</h2>
          {report.inboxes.length === 0 ? <p className="report-note">Nenhuma caixa neste recorte.</p> : <>
            <Bars unit="ciclos" rows={report.inboxes.map((row) => ({ key: row.inbox_id, label: row.name, value: row.cycles, detail: `${numberFormat.format(row.messages)} mensagens` }))} />
            <ul className="sr-only">{report.inboxes.map((row) => <li key={row.inbox_id}>{row.name}: {row.cycles} ciclos, {row.messages} mensagens</li>)}</ul>
          </>}
        </section>
        {report.reasons.length > 0 && <section aria-label="Motivos" className="report-card"><h2>Motivo principal</h2>
          <Bars rows={report.reasons.map((row) => ({ key: row.name, label: row.name, value: row.count }))} />
          <ul className="sr-only">{report.reasons.map((row) => <li key={row.name}>{row.name}: {row.count}</li>)}</ul></section>}
        {report.tags.length > 0 && <section aria-label="Etiquetas" className="report-card"><h2>Etiquetas adicionais</h2><p className="report-note">Estas contagens se sobrepõem e não fecham o total.</p>
          <Bars rows={report.tags.map((row) => ({ key: row.name, label: row.name, value: row.count }))} />
          <ul className="sr-only">{report.tags.map((row) => <li key={row.name}>{row.name}: {row.count}</li>)}</ul></section>}
      </div>
      <Heatmap timezone={report.timezone} cells={report.heatmap} />
      {report.definitions.length > 0 && <section aria-label="Definições" className="report-card report-definitions"><h2>Como ler</h2><ul>{report.definitions.map((line) => <li key={line}>{line}</li>)}</ul></section>}
    </>}
  </>;
}
