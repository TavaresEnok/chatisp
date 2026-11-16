import { ArrowLeft, Plus } from "lucide-react";
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
  // Em telas estreitas a lista e o setor se alternam; em telas largas ficam lado a lado.
  const [detailOpen, setDetailOpen] = useState(false);
  async function run(fn: () => Promise<unknown>) { setBusy(true); setError(""); try { await fn(); await client.invalidateQueries({ queryKey: key }); } catch { setError("Não foi possível concluir a operação. Verifique os dados e vínculos."); } finally { setBusy(false); } }
  function create(e: FormEvent) { e.preventDefault(); void run(async () => { const sector = await api.create(name); setSelected(sector.id); setName(sector.name); }); }
  const list = sectors.data?.sectors ?? [];
  return <article className="split-page teams-page" data-detail={detailOpen}>
    <section className="split-list" aria-label="Setores da equipe">
      <header className="split-list-head"><div><h1>Equipes</h1><h2 className="split-count">Setores{sectors.isSuccess ? <> · <b>{list.length}</b></> : null}</h2></div>{principal.role === "admin" && <button type="button" className="split-new" disabled={busy} onClick={() => { setSelected(""); setName(""); setTarget(""); setError(""); setDetailOpen(true); }}><Plus size={16} strokeWidth={1.8} aria-hidden="true" />Novo setor</button>}</header>
      <div className="split-results">
        {sectors.isPending ? <p role="status">Carregando setores…</p> : sectors.isError ? <div role="alert" className="split-alert">Não foi possível carregar os setores. <button type="button" onClick={() => void sectors.refetch()}>Tentar novamente setores</button></div> : !list.length ? <p>Nenhum setor disponível.</p> : <ul className="split-rows sector-rows">{list.map(s => <li key={s.id} data-current={selected === s.id ? "true" : undefined} data-active={s.active ? "true" : "false"}><button type="button" disabled={busy} onClick={() => { setSelected(s.id); setName(s.name); setTarget(""); setError(""); setDetailOpen(true); }}>{s.name} — {s.active ? "Ativo" : "Inativo"}</button></li>)}</ul>}
      </div>
    </section>
    <section className="split-detail" aria-label="Setor">
      <header className="split-detail-head">
        <button type="button" className="split-back" title="Lista de setores" onClick={() => setDetailOpen(false)}><ArrowLeft size={16} aria-hidden="true" /><span className="visually-hidden">Lista de setores</span></button>
        <div className="split-detail-title">{current ? <><p className="split-detail-name">{current.name}</p><p className="split-detail-sub">{current.active ? "Setor ativo" : "Setor inativo"}</p></> : <><p className="split-detail-name">Setores</p><p className="split-detail-sub">Escolha um setor na lista para ver os vínculos.</p></>}</div>
      </header>
      <div className="split-detail-body" data-columns={selected ? "2" : "1"}>
        {principal.role === "admin" && <div className="detail-card"><h3>{current ? "Nome e situação" : "Novo setor"}</h3><form className="sector-form" onSubmit={create}><label>Nome do setor <input required value={name} onChange={e => setName(e.target.value)} /></label><div className="detail-buttons"><button disabled={busy}>Criar setor</button>{current && <><button type="button" disabled={busy} onClick={() => void run(() => api.update(selected, { name }))}>Renomear setor</button><button type="button" disabled={busy} onClick={() => void run(() => api.update(selected, { active: !current.active }))}>{current.active ? "Desativar setor" : "Ativar setor"}</button></>}</div></form></div>}
        {selected && <section className="detail-card sector-links"><h2>Vínculos do setor</h2>{details.isPending ? <p role="status">Carregando vínculos…</p> : details.isError ? <div role="alert">Não foi possível carregar os vínculos. <button type="button" onClick={() => void details.refetch()}>Tentar novamente vínculos</button></div> : <><p className="context-label">Vínculos de membros</p><ul>{details.data.membership_ids?.map(id => <li key={id}><code>{id}</code> {principal.role === "admin" && <button type="button" disabled={busy} onClick={() => void run(() => api.link(selected, id, "membership", "remove"))}>Remover membro</button>}</li>)}</ul><p className="context-label">Caixas vinculadas</p><ul>{details.data.inbox_ids?.map(id => <li key={id}><code>{id}</code> {details.data.default_inbox_ids?.includes(id) && "— Padrão"} {principal.role === "admin" && <><button type="button" disabled={busy} onClick={() => void run(() => api.link(selected, id, "inbox", "default"))}>Definir padrão</button><button type="button" disabled={busy} onClick={() => void run(() => api.link(selected, id, "inbox", "remove"))}>Remover caixa</button></>}</li>)}</ul></>}
          {principal.role === "admin" && <><label>Identificador do membro ou caixa <input value={target} onChange={e => setTarget(e.target.value)} /></label><div className="detail-buttons"><button type="button" disabled={busy || !target} onClick={() => void run(() => api.link(selected, target, "membership", "add"))}>Vincular membro</button><button type="button" disabled={busy || !target} onClick={() => void run(() => api.link(selected, target, "inbox", "add"))}>Vincular caixa</button></div></>}
        </section>}
        {principal.role !== "admin" && !selected && <div className="split-empty"><h2>Setores da equipe</h2><p>Os vínculos de cada setor aparecem aqui.</p></div>}
      </div>
      {error && <p role="alert" className="split-error">{error}</p>}
    </section>
  </article>;
}
