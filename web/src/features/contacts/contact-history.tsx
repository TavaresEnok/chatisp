import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import * as api from "@/api/contacts";
import { queryKeys } from "@/lib/query-keys";

function statusLabel(status: string) {
  switch (status) {
    case "open": return "Aberta";
    case "closed": return "Encerrada";
    case "waiting_customer": return "Aguardando cliente";
    default: return "Status desconhecido";
  }
}

export function ContactHistory({ contactId, scope }: { contactId: string; scope: readonly string[] }) {
  const [offset, setOffset] = useState(0);
  const history = useQuery({ queryKey: [...queryKeys.contactHistory(scope), contactId, offset], queryFn: () => api.history(contactId, offset), retry: false });
  return <aside aria-label="Histórico do contato"><h2>Histórico do contato</h2>
    {history.isPending ? <p role="status">Carregando histórico…</p> : history.isError ? <p role="alert">Não foi possível carregar o histórico autorizado.</p> : <>
      {!history.data.items?.length ? <p>Nenhuma conversa no histórico autorizado.</p> : <ul>{history.data.items.map(item => <li key={item.id}>{item.protocol} — {statusLabel(item.status)}</li>)}</ul>}
      <button type="button" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - 50))}>Anterior</button>
      <button type="button" disabled={history.data.next_offset === null} onClick={() => setOffset(history.data.next_offset!)}>Próxima</button>
    </>}
  </aside>;
}
