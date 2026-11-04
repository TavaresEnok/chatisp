import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { listAudit, type AuditEvent } from "@/api/audit-view";
import { formatWhen } from "@/lib/format";
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

export function AuditLog({ principal }: { principal: PanelPrincipal }) {
  const range = initialRange();
  const [from, setFrom] = useState(range.from);
  const [to, setTo] = useState(range.to);
  const [extra, setExtra] = useState<AuditEvent[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [moreError, setMoreError] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const enabled = principal.role === "admin" && !principal.mfaEnrollmentRequired;
  const query = useQuery({
    queryKey: ["audit-view", principal.sessionId, principal.tenantId, from, to],
    queryFn: ({ signal }) => listAudit({ from, to }, signal),
    retry: false,
    enabled,
  });

  if (!enabled) return null;

  const events = [...(query.data?.events ?? []), ...extra];
  const next = query.data ? (cursor === null ? query.data.next_cursor : cursor || undefined) : undefined;

  async function showMore() {
    const token = query.data ? (cursor === null ? query.data.next_cursor : cursor || undefined) : undefined;
    if (!token || loadingMore) return;
    const requested = { from, to, token };
    setLoadingMore(true);
    setMoreError(false);
    try {
      const page = await listAudit({ from: requested.from, to: requested.to, cursor: requested.token });
      if (requested.from !== from || requested.to !== to) return;
      setExtra((current) => [...current, ...page.events]);
      setCursor(page.next_cursor ?? "");
    } catch {
      if (requested.from === from && requested.to === to) setMoreError(true);
    } finally {
      setLoadingMore(false);
    }
  }

  return (
    <section aria-label="Auditoria do provedor" className="flex flex-col gap-3">
      <h2 className="text-base font-medium">Auditoria do provedor</h2>
      <form className="flex flex-wrap gap-3" onSubmit={(event) => event.preventDefault()}>
        <label>
          Início{" "}
          <input
            type="date"
            value={from}
            onChange={(event) => {
              setFrom(event.target.value);
              setExtra([]);
              setCursor(null);
              setMoreError(false);
            }}
          />
        </label>
        <label>
          Fim{" "}
          <input
            type="date"
            value={to}
            onChange={(event) => {
              setTo(event.target.value);
              setExtra([]);
              setCursor(null);
              setMoreError(false);
            }}
          />
        </label>
      </form>
      {query.isPending && <p role="status">Carregando a auditoria…</p>}
      {query.isError && <p role="alert">Não foi possível carregar a auditoria.</p>}
      {query.data && events.length === 0 && <p>Nenhum evento neste período.</p>}
      {events.length > 0 && (
        <table>
          <caption className="sr-only">Eventos de auditoria</caption>
          <thead>
            <tr>
              <th>Data</th>
              <th>Ação</th>
              <th>Tipo do alvo</th>
              <th>Resultado</th>
            </tr>
          </thead>
          <tbody>
            {events.map((event, index) => (
              <tr key={`${event.created_at}-${event.action}-${event.target_type}-${event.result}-${index}`}>
                <td>{formatWhen(event.created_at)}</td>
                <td>{event.action}</td>
                <td>{event.target_type}</td>
                <td>{event.result}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {next && (
        <button type="button" disabled={loadingMore} onClick={() => void showMore()}>
          Mostrar mais
        </button>
      )}
      {moreError && <p role="alert">Não foi possível carregar a auditoria.</p>}
    </section>
  );
}
