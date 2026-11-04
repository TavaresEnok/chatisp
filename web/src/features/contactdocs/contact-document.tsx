import { useEffect, useRef, useState, type FormEvent } from "react";
import * as api from "@/api/contactdocs";
import { PanelRequestError } from "@/api/problems";
import { presenceScope } from "@/features/presence/operator-presence";
import { useSessionStore, type PanelPrincipal } from "@/stores/session-store";

export function documentMask(kind?: string) { return kind === "cnpj" ? "**.***.***/****-**" : "***.***.***-**"; }
export function ContactDocument({ principal, contactId }: { principal: PanelPrincipal; contactId: string }) {
  const current = useSessionStore(s => s.principal);
  if (!current || current.mfaEnrollmentRequired || presenceScope(current) !== presenceScope(principal)) return null;
  return <DocumentBody key={`${presenceScope(principal)}:${contactId}`} principal={principal} contactId={contactId} />;
}
function DocumentBody({ principal, contactId }: { principal: PanelPrincipal; contactId: string }) {
  const [view, setView] = useState<api.DocumentView | null>(null);
  const [input, setInput] = useState(""); const [revealed, setRevealed] = useState("");
  const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  const controller = useRef<AbortController | null>(null);
  const canWrite = ["admin", "supervisor"].includes(principal.role);
  useEffect(() => {
    const active = new AbortController(); controller.current = active;
    const unsubscribe = useSessionStore.subscribe(() => { const p = useSessionStore.getState().principal; if (!p || p.mfaEnrollmentRequired || presenceScope(p) !== presenceScope(principal)) active.abort(); });
    void api.masked(contactId, active.signal).then(v => { if (!active.signal.aborted) setView(v); }).catch(() => { if (!active.signal.aborted) setError("Não foi possível consultar o documento."); });
    return () => { active.abort(); unsubscribe(); };
  }, [principal, contactId]);
  useEffect(() => { if (!revealed) return; const timer = window.setTimeout(() => setRevealed(""), 15_000); const hide = () => { if (document.hidden) setRevealed(""); }; document.addEventListener("visibilitychange", hide); return () => { clearTimeout(timer); document.removeEventListener("visibilitychange", hide); }; }, [revealed]);
  async function run(action: "save" | "clear" | "reveal") {
    const active = controller.current; if (!view || !active || active.signal.aborted || busy) return;
    setBusy(true); setError(""); setRevealed("");
    const value = input; setInput("");
    try {
      if (action === "reveal") { const result = await api.reveal(contactId, active.signal); if (!active.signal.aborted) setRevealed(result.document); }
      else { const result = action === "save" ? await api.setDocument(contactId, view.version, value, active.signal) : await api.clearDocument(contactId, view.version, active.signal); if (!active.signal.aborted) setView(result); }
    } catch (e) {
      if (!active.signal.aborted) {
        setError(e instanceof PanelRequestError && e.status === 403 ? "Acesso negado ao documento." : "Não foi possível concluir. Atualize os dados e tente novamente.");
        // A denial invalidates the previous projection and any revealed value.
        if (e instanceof PanelRequestError && [403, 404].includes(e.status)) setView(null);
        else { try { const latest = await api.masked(contactId, active.signal); if (!active.signal.aborted) setView(latest); } catch { /* Keep the generic error. */ } }
      }
    } finally { if (!active.signal.aborted) setBusy(false); }
  }
  return <section aria-label="Documento do contato"><h3>CPF/CNPJ</h3>
    {view ? <p>{view.present ? documentMask(view.kind) : "Documento não informado"}</p> : !error && <p role="status">Carregando documento…</p>}
    {canWrite && view && <><form onSubmit={(e: FormEvent) => { e.preventDefault(); void run("save"); }}><label>Definir CPF/CNPJ <input type="password" autoComplete="off" value={input} onChange={e => setInput(e.target.value)} /></label><button disabled={busy || !input}>Salvar documento</button></form>
      {view.present && <><button type="button" disabled={busy} onClick={() => void run("clear")}>Limpar documento</button><button type="button" disabled={busy} onClick={() => void run("reveal")}>Revelar por 15 segundos</button></>}
    </>}
    {revealed && <div aria-label="Documento revelado"><output>{revealed}</output><button type="button" onClick={() => setRevealed("")}>Ocultar documento</button></div>}
    {error && <p role="alert">{error}</p>}
  </section>;
}

export function DocumentSearch({ onFound }: { onFound: (id: string) => void }) {
  const principal = useSessionStore(s => s.principal);
  if (!principal || principal.mfaEnrollmentRequired) return null;
  return <SearchBody key={presenceScope(principal)} onFound={onFound} />;
}
function SearchBody({ onFound }: { onFound: (id: string) => void }) {
  const [value, setValue] = useState(""); const [busy, setBusy] = useState(false); const [error, setError] = useState("");
  const controller = useRef<AbortController | null>(null);
  useEffect(() => { const active = new AbortController(); controller.current = active; const initial = useSessionStore.getState().principal; const unsubscribe = useSessionStore.subscribe(() => { const current = useSessionStore.getState().principal; if (!initial || !current || current.mfaEnrollmentRequired || presenceScope(initial) !== presenceScope(current)) active.abort(); }); return () => { active.abort(); unsubscribe(); }; }, []);
  async function search(e: FormEvent) {
    e.preventDefault(); const active = controller.current; if (!active || active.signal.aborted) return;
    const document = value; setValue(""); setBusy(true); setError("");
    try { const result = await api.findDocument(document, active.signal); if (!active.signal.aborted) onFound(result.contact_id); }
    catch { if (!active.signal.aborted) setError("Documento não encontrado ou acesso indisponível."); }
    finally { if (!active.signal.aborted) setBusy(false); }
  }
  return <form onSubmit={e => void search(e)} aria-label="Busca exata por documento"><label>Buscar CPF/CNPJ exato <input type="password" autoComplete="off" value={value} onChange={e => setValue(e.target.value)} /></label><button disabled={!value || busy}>Buscar documento</button>{error && <p role="alert">{error}</p>}</form>;
}
