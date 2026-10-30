import { useEffect, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate } from "react-router";
import * as api from "@/api/sectors";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";
export function TeamsPage() {
  const p = useSessionStore(s => s.principal);
  if (!p || p.mfaEnrollmentRequired) return <Navigate to="/entrar" replace />;
  if (!["admin", "supervisor", "agent"].includes(p.role)) return <p>Acesso restrito ao atendimento.</p>;
  return <Sectors key={JSON.stringify([p.tenantId, p.userId, p.sessionId, p.role, p.membershipId])} principal={p} />;
}
function Sectors({ principal }: { principal: PanelPrincipal }) {
  const scope = JSON.stringify([principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId]);
  const client = useQueryClient(); const [selected, setSelected] = useState(""); const [name, setName] = useState(""); const [target, setTarget] = useState(""); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  const key = ["m3", scope, "sectors"];
  useEffect(() => () => { client.removeQueries({ queryKey: ["m3", scope, "sectors"] }); }, [client, scope]);
  const sectors = useQuery({ queryKey: key, queryFn: api.list, retry: false });
  const details = useQuery({ queryKey: [...key, selected], queryFn: () => api.details(selected), enabled: !!selected, retry: false });
  const current = sectors.data?.sectors?.find(s => s.id === selected);
  async function run(fn: () => Promise<unknown>) { setBusy(true); setError(""); try { await fn(); await client.invalidateQueries({ queryKey: key }); } catch { setError("Não foi possível concluir a operação. Verifique os dados e vínculos."); } finally { setBusy(false); } }
  function create(e: FormEvent) { e.preventDefault(); void run(async () => { const sector = await api.create(name); setSelected(sector.id); setName(sector.name); }); }
  return <article><h1>Equipes</h1><h2>Setores</h2>
    {sectors.isPending ? <p role="status">Carregando setores…</p> : sectors.isError ? <p role="alert">Não foi possível carregar os setores.</p> : !sectors.data.sectors?.length ? <p>Nenhum setor disponível.</p> : <ul>{sectors.data.sectors.map(s => <li key={s.id}><button type="button" onClick={() => { setSelected(s.id); setName(s.name); setError(""); }}>{s.name} — {s.active ? "Ativo" : "Inativo"}</button></li>)}</ul>}
    {principal.role === "admin" && <form onSubmit={create}><label>Nome do setor <input required value={name} onChange={e => setName(e.target.value)} /></label><button disabled={busy}>Criar setor</button>{current && <><button type="button" disabled={busy} onClick={() => void run(() => api.update(selected, { name }))}>Renomear setor</button><button type="button" disabled={busy} onClick={() => void run(() => api.update(selected, { active: !current.active }))}>{current.active ? "Desativar setor" : "Ativar setor"}</button></>}</form>}
    {selected && <section><h2>Vínculos do setor</h2>{details.isPending ? <p role="status">Carregando vínculos…</p> : details.isError ? <p role="alert">Não foi possível carregar os vínculos.</p> : <><p>Vínculos de membros</p><ul>{details.data.membership_ids?.map(id => <li key={id}>{id} {principal.role === "admin" && <button type="button" disabled={busy} onClick={() => void run(() => api.link(selected, id, "membership", "remove"))}>Remover membro</button>}</li>)}</ul><p>Caixas vinculadas</p><ul>{details.data.inbox_ids?.map(id => <li key={id}>{id} {details.data.default_inbox_ids?.includes(id) && "— Padrão"} {principal.role === "admin" && <><button type="button" disabled={busy} onClick={() => void run(() => api.link(selected, id, "inbox", "default"))}>Definir padrão</button><button type="button" disabled={busy} onClick={() => void run(() => api.link(selected, id, "inbox", "remove"))}>Remover caixa</button></>}</li>)}</ul></>}
      {principal.role === "admin" && <><label>Identificador do membro ou caixa <input value={target} onChange={e => setTarget(e.target.value)} /></label><button type="button" disabled={busy || !target} onClick={() => void run(() => api.link(selected, target, "membership", "add"))}>Vincular membro</button><button type="button" disabled={busy || !target} onClick={() => void run(() => api.link(selected, target, "inbox", "add"))}>Vincular caixa</button></>}
    </section>}{error && <p role="alert">{error}</p>}
  </article>;
}
