import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { getMetaUsage } from "@/api/retention";

const BILLABLE: Record<string, string> = { true: "Cobrável", false: "Não cobrável", unknown: "Indicador desconhecido" };

function isoDay(date: Date): string {
  return date.toISOString().slice(0, 10);
}

/** Uso reportado pela Meta nos status de entrega. Contagem por categoria; sem valor em dinheiro. */
export function MetaUsageView({ scope }: { scope: string }) {
  const today = new Date();
  const [from, setFrom] = useState(isoDay(new Date(today.getTime() - 29 * 86_400_000)));
  const [to, setTo] = useState(isoDay(today));
  const valid = /^\d{4}-\d{2}-\d{2}$/.test(from) && /^\d{4}-\d{2}-\d{2}$/.test(to) && from <= to;
  const query = useQuery({
    queryKey: ["meta-usage", scope, from, to],
    queryFn: ({ signal }) => getMetaUsage(from, to, signal),
    enabled: valid,
    retry: false,
  });
  return (
    <section aria-label="Uso reportado pela Meta" className="flex flex-col gap-3">
      <h2 className="text-lg font-medium">Uso reportado pela Meta</h2>
      <div className="flex flex-wrap gap-3">
        <label className="flex min-w-0 flex-col gap-1 text-sm">De (uso da Meta)
          <input type="date" value={from} onChange={(event) => setFrom(event.target.value)} className="min-h-11 rounded-lg border border-input px-2" />
        </label>
        <label className="flex min-w-0 flex-col gap-1 text-sm">Até (uso da Meta)
          <input type="date" value={to} onChange={(event) => setTo(event.target.value)} className="min-h-11 rounded-lg border border-input px-2" />
        </label>
      </div>
      {!valid && <p role="alert" className="text-sm">Informe um período válido, com início até o fim.</p>}
      {valid && query.isPending && <p role="status" className="text-sm">Carregando o uso reportado…</p>}
      {valid && query.isError && <p role="alert" className="text-sm">Não foi possível carregar o uso reportado pela Meta.</p>}
      {query.data && (
        <>
          <p className="text-sm text-muted-foreground">{query.data.note}</p>
          {query.data.rows.length === 0 && query.data.without_pricing === 0 ? (
            <p className="text-sm">Nenhum status de entrega neste período.</p>
          ) : (
            <table className="text-sm">
              <caption className="sr-only">Mensagens por categoria e indicador de cobrança</caption>
              <thead><tr><th scope="col">Categoria</th><th scope="col">Cobrança</th><th scope="col">Mensagens</th></tr></thead>
              <tbody>
                {query.data.rows.map((row) => (
                  <tr key={`${row.category}:${row.billable}`}>
                    <td>{row.category}</td><td>{BILLABLE[row.billable] ?? row.billable}</td><td>{row.messages}</td>
                  </tr>
                ))}
                <tr><td>Sem informação de preço</td><td>Desconhecido</td><td>{query.data.without_pricing}</td></tr>
              </tbody>
            </table>
          )}
        </>
      )}
    </section>
  );
}
