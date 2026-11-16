import { ArrowLeft, Ban, Plus, Search } from "lucide-react";
import { ContactAvatar } from "@/components/ui/contact-avatar";
import { ContactDocument, DocumentSearch } from "@/features/contactdocs/contact-document";
import { useEffect, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate } from "react-router";
import * as api from "@/api/contacts";
import type { components } from "@/api/schema";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";

export function ContactsPage() {
  const p = useSessionStore(s => s.principal);
  if (!p || p.mfaEnrollmentRequired) return <Navigate to="/entrar" replace />;
  if (!["admin", "supervisor", "agent"].includes(p.role)) return <p>Acesso restrito ao atendimento.</p>;
  return <Directory key={JSON.stringify([p.tenantId, p.userId, p.sessionId, p.role, p.membershipId])} principal={p} />;
}
function Directory({ principal }: { principal: PanelPrincipal }) {
  const scope = JSON.stringify([principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId]);
  const client = useQueryClient(); const [q, setQ] = useState(""); const [offset, setOffset] = useState(0);
  useEffect(() => () => { client.removeQueries({ queryKey: ["m3", scope, "contacts"] }); }, [client, scope]);
  const [editing, setEditing] = useState<components["schemas"]["DirectoryContact"] | null>(null);
  const [name, setName] = useState(""); const [phone, setPhone] = useState(""); const [email, setEmail] = useState(""); const [notes, setNotes] = useState(""); const [reason, setReason] = useState(""); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  // Em telas estreitas a lista e a ficha se alternam; em telas largas ficam lado a lado.
  const [detailOpen, setDetailOpen] = useState(false);
  const [searchError, setSearchError] = useState("");
  const query = useQuery({ queryKey: ["m3", scope, "contacts", q, offset], queryFn: () => api.list(q, offset), retry: false });
  function edit(c: components["schemas"]["DirectoryContact"] | null) { setEditing(c); setName(c?.name ?? ""); setPhone(c?.phone ?? ""); setEmail(c?.email ?? ""); setNotes(c?.notes ?? ""); setReason(""); setError(""); }
  async function run(fn: () => Promise<unknown>) { setBusy(true); setError(""); try { await fn(); await client.invalidateQueries({ queryKey: ["m3", scope, "contacts"] }); return true; } catch { setError("Não foi possível concluir a operação."); return false; } finally { setBusy(false); } }
  async function save(e: FormEvent) { e.preventDefault(); const body = { name, phone, email, notes }; if (await run(() => editing ? api.update(editing.id, body) : api.create(body))) edit(null); }
  const items = query.data?.items ?? [];
  return <article className="split-page contacts-page" data-detail={detailOpen}>
    <section className="split-list" aria-label="Diretório de contatos">
      <header className="split-list-head">
        <div><h1>Contatos</h1>{query.isSuccess && <p className="split-count"><b>{items.length}</b> nesta página</p>}</div>
        <button type="button" className="split-new" disabled={busy} onClick={() => { edit(null); setDetailOpen(true); }}><Plus size={16} strokeWidth={1.8} aria-hidden="true" />Novo contato</button>
      </header>
      <div className="split-tools">
        <label className="split-search"><Search size={16} strokeWidth={1.6} aria-hidden="true" /><span className="visually-hidden">Buscar contatos</span> <input type="search" placeholder="Buscar no diretório" value={q} onChange={e => { setQ(e.target.value); setOffset(0); }} /></label>
        <DocumentSearch onFound={id => { setSearchError(""); void api.get(id).then(c => { edit(c); setDetailOpen(true); }).catch(() => setSearchError("Não foi possível abrir o contato.")); }} />
        {searchError && <p role="alert" className="split-error">{searchError}</p>}
      </div>
      <div className="split-results">
        {query.isPending ? <p role="status">Carregando contatos…</p> : query.isError ? <p role="alert">Não foi possível carregar os contatos.</p> : <>
          {!items.length ? <p>Nenhum contato encontrado.</p> : <ul className="split-rows">{items.map(c => <li key={c.id} data-current={editing?.id === c.id ? "true" : undefined}><button type="button" disabled={busy} onClick={() => { edit(c); setDetailOpen(true); }}><ContactAvatar label={c.name} size={40} /> {c.name}</button> {c.phone ? <span className="split-row-sub">{c.phone}</span> : null}{c.blocked ? <span className="split-row-flag"><Ban size={12} strokeWidth={2} aria-hidden="true" /><span className="visually-hidden">— </span>Bloqueado</span> : ""}</li>)}</ul>}
          <div className="split-pages"><button type="button" disabled={!offset} onClick={() => setOffset(Math.max(0, offset - 50))}>Anterior</button><button type="button" disabled={query.data.next_offset === null} onClick={() => setOffset(query.data.next_offset!)}>Próxima</button></div>
        </>}
      </div>
    </section>
    <section className="split-detail" aria-label="Ficha do contato">
      {query.isError ? <div className="split-empty"><h2>Diretório indisponível</h2><p>A ficha do contato aparece aqui quando a lista puder ser carregada.</p></div> : <>
        <header className="split-detail-head">
          <button type="button" className="split-back" title="Lista de contatos" onClick={() => setDetailOpen(false)}><ArrowLeft size={16} aria-hidden="true" /><span className="visually-hidden">Lista de contatos</span></button>
          {editing ? <><ContactAvatar label={editing.name} size={36} /><div className="split-detail-title"><h2 className="visually-hidden">Editar contato</h2><p className="split-detail-name">{editing.name}</p>{editing.phone ? <p className="split-detail-sub">{editing.phone}</p> : null}</div>{editing.blocked ? <span className="split-row-flag"><Ban size={12} strokeWidth={2} aria-hidden="true" />Bloqueado</span> : null}</>
            : <div className="split-detail-title"><h2>Novo contato</h2><p className="split-detail-sub">Escolha um contato na lista para editar, ou preencha os dados para cadastrar.</p></div>}
        </header>
        <div className="split-detail-body" data-columns={editing ? "2" : "1"}>
          <div className="detail-card">
            <h3>Dados do contato</h3>
            <form onSubmit={e => void save(e)}>
              <fieldset disabled={busy}><label>Nome <input required value={name} onChange={e => setName(e.target.value)} /></label><label>Telefone internacional <input required={!editing} value={phone} onChange={e => setPhone(e.target.value)} /></label><label>E-mail <input type="email" value={email} onChange={e => setEmail(e.target.value)} /></label><label>Observações <textarea rows={3} value={notes} onChange={e => setNotes(e.target.value)} /></label><button className="detail-save">Salvar contato</button></fieldset>
            </form>
          </div>
          {editing && <div className="detail-stack">
            <div className="detail-card"><ContactDocument principal={principal} contactId={editing.id} /></div>
            <div className="detail-card">
              <h3>Bloqueio</h3>
              <p className="detail-hint">{editing.blocked ? (editing.block_reason ? `Contato bloqueado. Motivo registrado: ${editing.block_reason}` : "Contato bloqueado.") : "Contato sem bloqueio."}</p>
              <div className="detail-inline"><label>Motivo <input value={reason} onChange={e => setReason(e.target.value)} /></label><button type="button" disabled={busy} onClick={() => void run(async () => { const c = await api.block(editing.id, !editing.blocked, reason); setEditing(c); })}>{editing.blocked ? "Desbloquear" : "Bloquear"}</button></div>
            </div>
          </div>}
        </div>
      </>}
      {error && <p role="alert" className="split-error">{error}</p>}
    </section>
  </article>;
}
