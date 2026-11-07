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
  return <p>{title}: {value}</p>;
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
  return <div className="space-y-4">
    {mode === "full" && <form className="flex flex-wrap gap-3" onSubmit={(event) => event.preventDefault()}>
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
    <LiveBoard report={report} />
    {mode === "full" && <>
      <ul>{report.definitions.map((line) => <li key={line}>{line}</li>)}</ul>
      <p>Fuso {report.timezone}. Conversas {report.conversations}, ciclos {report.cycles}, mensagens {report.messages}.</p>
      {quiet && <p>Nenhum ciclo, conversa ou mensagem neste recorte.</p>}
      {metric("Primeira resposta humana", report.first_response, "sem dado no período")}
      {metric("Tempo até o encerramento", report.close_total, "sem encerramento no período")}
      {metric("Tempo operacional", report.close_operational, "sem encerramento no período")}
      {metric("Espera na fila", report.queue_wait, "sem espera encerrada no período")}
      {report.results.length > 0 && <section aria-label="Resultados"><h2>Resultados</h2><ul>{report.results.map((row) => <li key={`${row.result}-${row.author_kind}`}>{row.label}: {row.count}</li>)}</ul></section>}
      <section aria-label="Por setor">
        <h2>Por setor</h2>
        {report.sectors.length === 0 ? <p>Nenhum setor neste recorte.</p> : <ul>{report.sectors.map((row) => <li key={row.sector_id}>{row.name}: {row.cycles} ciclos, {row.queue_waits} esperas, mediana {formatDuration(row.queue_wait_median_seconds)}, responsabilidade {formatDuration(row.responsibility_seconds)}</li>)}</ul>}
      </section>
      <section aria-label="Por atendente">
        <h2>Por atendente</h2>
        {report.attendants.length === 0 ? <p>Nenhum atendente neste recorte.</p> : <ul>{report.attendants.map((row) => <li key={row.membership_id}>{row.label}: responsabilidade {formatDuration(row.responsibility_seconds)}, {row.messages} {row.messages === 1 ? "mensagem" : "mensagens"}, {row.closures} {row.closures === 1 ? "encerramento" : "encerramentos"}</li>)}</ul>}
      </section>
      <section aria-label="Por caixa">
        <h2>Por caixa</h2>
        {report.inboxes.length === 0 ? <p>Nenhuma caixa neste recorte.</p> : <ul>{report.inboxes.map((row) => <li key={row.inbox_id}>{row.name}: {row.cycles} ciclos, {row.messages} mensagens</li>)}</ul>}
      </section>
      {report.reasons.length > 0 && <section aria-label="Motivos"><h2>Motivo principal</h2><ul>{report.reasons.map((row) => <li key={row.name}>{row.name}: {row.count}</li>)}</ul></section>}
      {report.tags.length > 0 && <section aria-label="Etiquetas"><h2>Etiquetas adicionais</h2><p>Estas contagens se sobrepõem e não fecham o total.</p><ul>{report.tags.map((row) => <li key={row.name}>{row.name}: {row.count}</li>)}</ul></section>}
      <Heatmap timezone={report.timezone} cells={report.heatmap} />
      {report.buckets.length > 0 && <section aria-label="Agrupamento"><h2>Agrupamento</h2><ul>{report.buckets.map((row) => <li key={row.key}>{row.key}: {row.cycles} ciclos, {row.messages} mensagens, {row.closed} encerrados. Primeira resposta humana: {row.first_response_sample === 0 ? "sem amostra" : `${formatDuration(row.first_response_average_seconds)} (amostra ${row.first_response_sample})`}. Tempo operacional do agrupamento: {row.operational_sample === 0 ? "sem amostra" : `${formatDuration(row.operational_average_seconds)} (amostra ${row.operational_sample})`}.</li>)}</ul></section>}
      <button type="button" onClick={() => download(report.csv)}>Baixar CSV</button>
    </>}
  </>;
}
