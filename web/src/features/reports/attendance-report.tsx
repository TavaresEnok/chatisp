import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { getReport, type AttendanceReport, type ReportStats } from "@/api/reports";
import { presenceScope } from "@/features/presence/operator-presence";
import type { PanelPrincipal } from "@/stores/session-store";

const weekdays = ["domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"];

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
  return <section aria-label="Situação agora">
    <h2>Agora</h2>
    <ul>
      <li>Na fila: {live.in_queue}</li>
      <li>Em atendimento: {live.in_attendance}</li>
      <li>Aguardando o cliente: {live.waiting_customer}</li>
      <li>Adiadas: {live.snoozed}</li>
      <li>Sem responsável: {live.unassigned}</li>
      <li>Atendentes online: {live.online_attendants}</li>
      <li>Maior espera: {formatDuration(live.longest_queue_wait_seconds)}</li>
      <li>Alertas de espera: {live.wait_alerts}</li>
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

export function AttendanceReportView({ principal, mode }: { principal: PanelPrincipal; mode: "live" | "full" }) {
  const range = initialRange();
  const [from, setFrom] = useState(range.from);
  const [to, setTo] = useState(range.to);
  const [group, setGroup] = useState<"day" | "week" | "month">("day");
  const query = useQuery({
    queryKey: ["reports", presenceScope(principal), mode === "live" ? "hoje" : from, mode === "live" ? "hoje" : to, group],
    queryFn: ({ signal }) => getReport(mode === "live" ? { from: civil(new Date()), to: civil(new Date()), group: "day" } : { from, to, group }, signal),
    retry: false,
  });
  return <div className="space-y-4">
    {mode === "full" && <form className="flex flex-wrap gap-3" onSubmit={(event) => event.preventDefault()}>
      <label>Início <input type="date" value={from} onChange={(event) => setFrom(event.target.value)} /></label>
      <label>Fim <input type="date" value={to} onChange={(event) => setTo(event.target.value)} /></label>
      <label>Agrupamento <select value={group} onChange={(event) => setGroup(event.target.value as "day" | "week" | "month")}><option value="day">Dia</option><option value="week">Semana</option><option value="month">Mês</option></select></label>
    </form>}
    {query.isPending && <p role="status">Carregando o relatório…</p>}
    {query.isError && <p role="alert">Não foi possível carregar o relatório.</p>}
    {query.data && <ReportBody report={query.data} mode={mode} />}
  </div>;
}

function ReportBody({ report, mode }: { report: AttendanceReport; mode: "live" | "full" }) {
  return <>
    <LiveBoard report={report} />
    {mode === "full" && <>
      <ul>{report.definitions.map((line) => <li key={line}>{line}</li>)}</ul>
      <p>Fuso {report.timezone}. Conversas {report.conversations}, ciclos {report.cycles}, mensagens {report.messages}.</p>
      {metric("Primeira resposta humana", report.first_response, "sem dado no período")}
      {metric("Tempo até o encerramento", report.close_total, "sem encerramento no período")}
      {metric("Tempo operacional", report.close_operational, "sem encerramento no período")}
      {metric("Espera na fila", report.queue_wait, "sem espera encerrada no período")}
      {report.results.length > 0 && <section aria-label="Resultados"><h2>Resultados</h2><ul>{report.results.map((row) => <li key={`${row.result}-${row.author_kind}`}>{row.label}: {row.count}</li>)}</ul></section>}
      {report.sectors.length > 0 && <section aria-label="Por setor"><h2>Por setor</h2><ul>{report.sectors.map((row) => <li key={row.sector_id}>{row.name}: {row.cycles} ciclos, {formatDuration(row.responsibility_seconds)} de responsabilidade</li>)}</ul></section>}
      {report.attendants.length > 0 && <section aria-label="Por atendente"><h2>Por atendente</h2><ul>{report.attendants.map((row) => <li key={row.membership_id}>{row.label}: {formatDuration(row.responsibility_seconds)}, {row.messages} mensagens, {row.closures} encerramentos</li>)}</ul></section>}
      {report.inboxes.length > 0 && <section aria-label="Por caixa"><h2>Por caixa</h2><ul>{report.inboxes.map((row) => <li key={row.inbox_id}>{row.name}: {row.cycles} ciclos, {row.messages} mensagens</li>)}</ul></section>}
      {report.reasons.length > 0 && <section aria-label="Motivos"><h2>Motivo principal</h2><ul>{report.reasons.map((row) => <li key={row.name}>{row.name}: {row.count}</li>)}</ul></section>}
      {report.tags.length > 0 && <section aria-label="Etiquetas"><h2>Etiquetas adicionais</h2><p>Estas contagens se sobrepõem e não fecham o total.</p><ul>{report.tags.map((row) => <li key={row.name}>{row.name}: {row.count}</li>)}</ul></section>}
      {report.heatmap.length > 0 && <section aria-label="Mapa de calor"><h2>Mapa de calor</h2><ul>{report.heatmap.map((cell) => <li key={`${cell.weekday}-${cell.hour}`}>{weekdays[cell.weekday] ?? "dia"} {cell.hour}h: {cell.cycles}</li>)}</ul></section>}
      {report.buckets.length > 0 && <section aria-label="Agrupamento"><h2>Agrupamento</h2><ul>{report.buckets.map((row) => <li key={row.key}>{row.key}: {row.cycles} ciclos, {row.messages} mensagens, {row.closed} encerrados</li>)}</ul></section>}
      <button type="button" onClick={() => download(report.csv)}>Baixar CSV</button>
    </>}
  </>;
}
