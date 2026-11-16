import { Flag, Lock } from "lucide-react";
import { useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import * as api from "@/api/annotations";
import { AttendanceError } from "@/api/attendance";
import type { PanelPrincipal } from "@/stores/session-store";

export function AnnotationPanel({ principal, inbox, conversation, cursor, onChanged, labels }: { principal: PanelPrincipal; inbox: string; conversation: string; cursor: number; onChanged: () => void; labels?: ReadonlyMap<string, string> }) {
  const client = useQueryClient();
  const key = ["m3b-annotations", principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId, inbox, conversation];
  const state = useQuery({ queryKey: key, queryFn: () => api.annotations(inbox, conversation), retry: false });
  const catalog = useQuery({ queryKey: ["m3b-tags", principal.tenantId, principal.userId, principal.sessionId, principal.role, principal.membershipId], queryFn: api.tags, retry: false });
  const [note, setNote] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const noteIntent = useRef<{ text: string; id: string } | null>(null);
  const data = state.data;
  async function update(kind: string, value = "", subject_id = "00000000-0000-0000-0000-000000000000") {
    if (busy) return;
    const intent = kind === "note" ? noteIntent.current ?? { text: value, id: crypto.randomUUID() } : null;
    if (intent && intent.text !== value) { setMessage("Repita a nota anterior antes de iniciar outra."); return; }
    noteIntent.current = intent;
    setBusy(true); setMessage("");
    try {
      await api.changeAnnotation(inbox, conversation, { kind, value, subject_id, request_id: intent?.id ?? "" });
      noteIntent.current = null;
      if (kind === "note") setNote("");
      await client.invalidateQueries({ queryKey: key }); onChanged();
    } catch (error) {
      if (error instanceof AttendanceError) noteIntent.current = null;
      setMessage(error instanceof AttendanceError ? error.message : "Resposta perdida; repita a mesma ação.");
      if (error instanceof AttendanceError && error.status === 409) void client.invalidateQueries({ queryKey: key });
    } finally { setBusy(false); }
  }
  async function read(unread: boolean) {
    if (!data || busy) return;
    setBusy(true); setMessage("");
    try { await api.markRead(inbox, conversation, { watermark: unread ? data.watermark : Math.max(cursor, data.watermark), unread, read_version: data.read_version }); await client.invalidateQueries({ queryKey: key }); onChanged(); }
    catch (error) { setMessage(error instanceof AttendanceError ? error.message : "Não foi possível atualizar a leitura."); void client.invalidateQueries({ queryKey: key }); }
    finally { setBusy(false); }
  }
  if (state.isPending) return <p>Carregando anotações…</p>;
  if (state.isError || !data) return <p role="alert">Não foi possível carregar as anotações.</p>;
  return <section aria-label="Anotações privadas" className="annotation-panel">
    <p className="context-fact" data-priority={data.priority}><span>Prioridade<span className="visually-hidden">: </span></span><b>{data.priority === "urgent" && <Flag size={12} strokeWidth={2} aria-hidden="true" />}{data.priority === "urgent" ? "Urgente" : "Normal"}</b></p>
    <div className="context-links">
      {["admin", "supervisor"].includes(principal.role) && <button type="button" disabled={busy} onClick={() => void update("priority", data.priority === "urgent" ? "normal" : "urgent")}>Alterar prioridade</button>}
      <button type="button" disabled={busy} onClick={() => void read(false)}>Marcar como lida</button>
      <button type="button" disabled={busy} onClick={() => void read(true)}>Marcar como não lida</button>
    </div>
    <div className="tag-list"><span className="context-label">Tags<span className="visually-hidden">: </span></span>{(data.tags ?? []).map(tag => <button type="button" className="tag-chip" title={`Remover a tag ${tag.name}`} disabled={busy} key={tag.id} onClick={() => void update("tag_remove", "", tag.id)}>{tag.name} ×</button>)}</div>
    <label className="tag-add">Adicionar tag <select disabled={busy} value="" onChange={e => { if (e.target.value) void update("tag_add", "", e.target.value); }}><option value="">Selecione</option>{(catalog.data ?? []).filter(tag => !(data.tags ?? []).some(t => t.id === tag.id)).map(tag => <option key={tag.id} value={tag.id}>{tag.name}</option>)}</select></label>
    <h2 className="context-label"><Lock size={11} strokeWidth={2} aria-hidden="true" />Anotações privadas</h2>
    <ul className="note-list">{(data.notes ?? []).map(item => <li key={item.id}><span className="note-body">{item.body}</span> <span className="note-author">— {labels?.get(item.author_id) ?? (item.author_id === principal.membershipId ? principal.email : "Atendente indisponível")}</span></li>)}</ul>
    <label className="note-new">Nova nota privada <textarea rows={2} placeholder="Visível só para a equipe" value={note} onChange={e => setNote(e.target.value)} /></label>
    <button type="button" className="note-save" disabled={busy || !note.trim()} onClick={() => void update("note", note)}>Salvar nota</button>
    {message && <p role="status">{message}</p>}
  </section>;
}
