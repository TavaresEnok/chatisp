import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import * as api from "@/api/contacts";
import { queryKeys } from "@/lib/query-keys";

function statusLabel(status: string) {
  switch (status) {
    case "triage": return "Em triagem";
    case "open": return "Aberta";
    case "closed": return "Encerrada";
    case "waiting_customer": return "Aguardando cliente";
    default: return "Status desconhecido";
  }
}

const historyDate = new Intl.DateTimeFormat("pt-BR", { dateStyle: "medium", timeStyle: "short" });
function formatDate(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Data indisponível" : historyDate.format(date);
}

export function ContactHistory({ contactId, scope }: { contactId: string; scope: readonly string[] }) {
  const [offset, setOffset] = useState(0);
  const history = useQuery({ queryKey: [...queryKeys.contactHistory(scope), contactId, offset], queryFn: () => api.history(contactId, offset), retry: false });
  return <aside aria-label="Histórico do contato"><h2>Histórico do contato</h2><p className="contact-history-intro">Consulte os protocolos e a situação dos atendimentos deste contato.</p>
    {history.isPending ? <p role="status">Carregando histórico…</p> : history.isError ? <p role="alert">Não foi possível carregar o histórico autorizado.</p> : <>
      {!history.data.items?.length ? <p>Nenhuma conversa no histórico autorizado.</p> : <ol className="contact-history-list">{history.data.items.map(item => <li key={item.id}><strong className="contact-history-protocol">{item.protocol}</strong><span className="contact-history-state">{statusLabel(item.status)}</span><time dateTime={item.created_at}>Início: {formatDate(item.created_at)}</time><time dateTime={item.updated_at}>Atualização: {formatDate(item.updated_at)}</time></li>)}</ol>}
      <div className="contact-history-pages"><button type="button" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))}>Anterior</button>
      <button type="button" disabled={history.data.next_offset === null} onClick={() => setOffset(history.data.next_offset!)}>Próxima</button></div>
    </>}
  </aside>;
}
