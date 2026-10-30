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
  const query = useQuery({ queryKey: ["m3", scope, "contacts", q, offset], queryFn: () => api.list(q, offset), retry: false });
  function edit(c: components["schemas"]["DirectoryContact"] | null) { setEditing(c); setName(c?.name ?? ""); setPhone(c?.phone ?? ""); setEmail(c?.email ?? ""); setNotes(c?.notes ?? ""); setReason(""); setError(""); }
  async function run(fn: () => Promise<unknown>) { setBusy(true); setError(""); try { await fn(); await client.invalidateQueries({ queryKey: ["m3", scope, "contacts"] }); return true; } catch { setError("Não foi possível concluir a operação."); return false; } finally { setBusy(false); } }
  async function save(e: FormEvent) { e.preventDefault(); const body = { name, phone, email, notes }; if (await run(() => editing ? api.update(editing.id, body) : api.create(body))) edit(null); }
  return <article><h1>Contatos</h1><label>Buscar contatos <input value={q} onChange={e => { setQ(e.target.value); setOffset(0); }} /></label>
    {query.isPending ? <p role="status">Carregando contatos…</p> : query.isError ? <p role="alert">Não foi possível carregar os contatos.</p> : <>
      {!query.data.items?.length ? <p>Nenhum contato encontrado.</p> : <ul>{query.data.items.map(c => <li key={c.id}><button type="button" disabled={busy} onClick={() => edit(c)}>{c.name}</button> {c.blocked ? "— Bloqueado" : ""}</li>)}</ul>}
      <button type="button" disabled={!offset} onClick={() => setOffset(Math.max(0, offset - 50))}>Anterior</button><button type="button" disabled={query.data.next_offset === null} onClick={() => setOffset(query.data.next_offset!)}>Próxima</button>
    </>}
    {!query.isError && <><h2>{editing ? "Editar contato" : "Novo contato"}</h2><form onSubmit={e => void save(e)}>
      <fieldset disabled={busy}><label>Nome <input required value={name} onChange={e => setName(e.target.value)} /></label><label>Telefone internacional <input required={!editing} value={phone} onChange={e => setPhone(e.target.value)} /></label><label>E-mail <input type="email" value={email} onChange={e => setEmail(e.target.value)} /></label><label>Observações <textarea value={notes} onChange={e => setNotes(e.target.value)} /></label><button>Salvar contato</button>{editing && <button type="button" onClick={() => edit(null)}>Novo contato</button>}</fieldset>
    </form>{editing && <><label>Motivo <input value={reason} onChange={e => setReason(e.target.value)} /></label><button type="button" disabled={busy} onClick={() => void run(async () => { const c = await api.block(editing.id, !editing.blocked, reason); setEditing(c); })}>{editing.blocked ? "Desbloquear" : "Bloquear"}</button></>}</>}{error && <p role="alert">{error}</p>}
  </article>;
}
