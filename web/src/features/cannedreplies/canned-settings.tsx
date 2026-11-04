import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createReply, deleteReply, getSignature, listReplies, saveSignature, updateReply, type CannedReply, type CannedSignature } from "@/api/cannedreplies";
import { PanelRequestError } from "@/api/problems";
import { cannedKey, cannedScopeToken, denyCannedScope, forgetCannedQueries, subscribeCannedDenial } from "@/features/cannedreplies/cache";
import { CLOSED_VARIABLES, prefixError, replyError } from "@/features/cannedreplies/rules";
import type { PanelPrincipal } from "@/stores/session-store";

function canWrite(role: string): boolean {
  return role === "admin" || role === "supervisor";
}

function denied(error: unknown): boolean {
  return error instanceof PanelRequestError && [403, 404].includes(error.status);
}

function invalidated(): DOMException {
  return new DOMException("A consulta foi invalidada.", "AbortError");
}

function denialMessage(error: unknown): string {
  return error instanceof PanelRequestError ? error.message : "Acesso negado.";
}

export function CannedSettings({ principal }: { principal: PanelPrincipal }) {
  const [open, setOpen] = useState(false);
  if (!open) {
    return <button type="button" onClick={() => setOpen(true)}>Mostrar respostas compartilhadas</button>;
  }
  return <CannedBody key={cannedScopeToken(principal)} principal={principal} />;
}

function CannedBody({ principal }: { principal: PanelPrincipal }) {
  const client = useQueryClient();
  const revokedRef = useRef(false);
  const generation = useRef(0);
  const [revoked, setRevoked] = useState(false);
  const [denial, setDenial] = useState("");
  const [search, setSearch] = useState("");
  const [applied, setApplied] = useState("");
  const [offset, setOffset] = useState(0);
  const token = cannedScopeToken(principal);
  const key = cannedKey(principal, "catalog", applied, String(offset));
  const query = useQuery({
    queryKey: key,
    queryFn: async ({ signal }) => {
      if (revokedRef.current) throw invalidated();
      const stamp = generation.current;
      const page = await listReplies(applied, 25, offset, signal);
      if (revokedRef.current || stamp !== generation.current || signal.aborted) throw invalidated();
      return page;
    },
    enabled: !revoked,
    retry: false,
  });
  useEffect(() => () => {
    generation.current += 1;
    forgetCannedQueries(client, principal);
  }, [client, principal]);
  useEffect(() => subscribeCannedDenial((deniedToken, message) => {
    if (deniedToken !== token) return;
    revokedRef.current = true;
    generation.current += 1;
    setRevoked(true);
    setDenial(message);
  }), [token]);
  useEffect(() => {
    if (denied(query.error)) denyCannedScope(client, principal, denialMessage(query.error));
  }, [client, principal, query.error]);
  function onDenied(error: unknown) {
    denyCannedScope(client, principal, denialMessage(error));
  }
  if (revoked) {
    return (
      <section aria-label="Respostas compartilhadas">
        <h2>Respostas compartilhadas</h2>
        <p>{denial}</p>
      </section>
    );
  }
  const writable = canWrite(principal.role);
  return (
    <section aria-label="Respostas compartilhadas">
      <h2>Respostas compartilhadas</h2>
      <details open>
        <summary>Catálogo e assinatura</summary>
      <p>O catálogo é do provedor. O texto renderizado entra no rascunho e só é enviado pelo compositor já existente. Variáveis permitidas: {CLOSED_VARIABLES.join(", ")}.</p>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          setOffset(0);
          setApplied(search.trim().toLowerCase());
        }}
      >
        <label>
          Buscar respostas
          <input value={search} maxLength={120} onChange={(event) => setSearch(event.target.value)} />
        </label>
        <button type="submit">Buscar</button>
      </form>
      {query.isPending ? <p>Carregando respostas…</p> : null}
      {query.isError ? <p>Não foi possível carregar as respostas.</p> : null}
      <ul>
        {(query.data?.replies ?? []).map((reply) => (
          <li key={reply.id}>
            <ReplyRow reply={reply} writable={writable} onDenied={onDenied} onChanged={() => void client.invalidateQueries({ queryKey: cannedKey(principal, "catalog") })} />
          </li>
        ))}
      </ul>
      {query.data?.has_more ? <button type="button" onClick={() => setOffset((value) => value + 25)}>Próxima página</button> : null}
      {writable ? <ReplyEditor onDenied={onDenied} onSaved={() => void client.invalidateQueries({ queryKey: cannedKey(principal, "catalog") })} /> : <p>Seu papel consulta e usa as respostas. Alterar o catálogo cabe a administrador ou supervisor.</p>}
      <SignatureSettings principal={principal} writable={writable} onDenied={onDenied} />
      </details>
    </section>
  );
}

function ReplyRow({ reply, writable, onChanged, onDenied }: { reply: CannedReply; writable: boolean; onChanged: () => void; onDenied: (error: unknown) => void }) {
  const [editing, setEditing] = useState(false);
  const [message, setMessage] = useState("");
  async function remove() {
    setMessage("");
    try {
      await deleteReply(reply.id, reply.version);
      onChanged();
    } catch (error) {
      if (denied(error)) {
        onDenied(error);
        return;
      }
      setMessage(error instanceof PanelRequestError ? error.message : "Não foi possível excluir.");
    }
  }
  return (
    <article>
      <p>/{reply.shortcode} — {reply.title}</p>
      <pre>{reply.content}</pre>
      {writable ? <button type="button" onClick={() => setEditing((value) => !value)}>Editar resposta</button> : null}
      {writable ? <button type="button" onClick={() => void remove()}>Excluir resposta</button> : null}
      {editing ? <ReplyEditor initial={reply} onDenied={onDenied} onSaved={() => { setEditing(false); onChanged(); }} /> : null}
      {message ? <p>{message}</p> : null}
    </article>
  );
}

function ReplyEditor({ initial, onSaved, onDenied }: { initial?: CannedReply; onSaved: () => void; onDenied: (error: unknown) => void }) {
  const [shortcode, setShortcode] = useState(initial?.shortcode ?? "");
  const [title, setTitle] = useState(initial?.title ?? "");
  const [content, setContent] = useState(initial?.content ?? "");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit() {
    const error = replyError({ shortcode, title, content });
    if (error) {
      setMessage(error);
      return;
    }
    setBusy(true);
    setMessage("");
    try {
      if (initial) await updateReply(initial.id, { shortcode, title, content, version: initial.version });
      else await createReply({ shortcode, title, content });
      if (!initial) {
        setShortcode("");
        setTitle("");
        setContent("");
      }
      onSaved();
    } catch (error) {
      if (denied(error)) {
        onDenied(error);
        return;
      }
      setMessage(error instanceof PanelRequestError ? error.message : "Não foi possível gravar a resposta.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form
      aria-label={initial ? "Editar resposta" : "Nova resposta"}
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <label>Atalho <input value={shortcode} onChange={(event) => setShortcode(event.target.value.toLowerCase())} /></label>
      <label>Título <input value={title} onChange={(event) => setTitle(event.target.value)} /></label>
      <label>Conteúdo <textarea value={content} onChange={(event) => setContent(event.target.value)} /></label>
      <p>
        {CLOSED_VARIABLES.map((name) => (
          <button key={name} type="button" onClick={() => setContent((value) => `${value}{{${name}}}`)}>
            Inserir {name}
          </button>
        ))}
      </p>
      <button type="submit" disabled={busy}>{initial ? "Atualizar resposta" : "Criar resposta"}</button>
      {message ? <p>{message}</p> : null}
    </form>
  );
}

function SignatureSettings({ principal, writable, onDenied }: { principal: PanelPrincipal; writable: boolean; onDenied: (error: unknown) => void }) {
  const client = useQueryClient();
  const revokedRef = useRef(false);
  const closedRef = useRef(false);
  const generation = useRef(0);
  const generationListeners = useRef(new Set<() => void>());
  const subscribeGeneration = useCallback((listener: () => void) => {
    generationListeners.current.add(listener);
    return () => { generationListeners.current.delete(listener); };
  }, []);
  const generationStamp = useCallback(() => generation.current, []);
  const stamp = useSyncExternalStore(subscribeGeneration, generationStamp);
  const bumpGeneration = useCallback(() => {
    generation.current += 1;
    for (const listener of generationListeners.current) listener();
  }, []);
  const [revoked, setRevoked] = useState(false);
  const key = cannedKey(principal, "signature");
  const token = cannedScopeToken(principal);
  const query = useQuery({
    queryKey: key,
    queryFn: async ({ signal }) => {
      if (revokedRef.current || closedRef.current) throw invalidated();
      const seen = generation.current;
      const signature = await getSignature(signal);
      if (revokedRef.current || closedRef.current || seen !== generation.current || signal.aborted) throw invalidated();
      return signature;
    },
    enabled: !revoked,
    retry: false,
  });
  useEffect(() => {
    closedRef.current = false;
    return () => {
      closedRef.current = true;
      bumpGeneration();
    };
  }, [bumpGeneration]);
  useEffect(() => subscribeCannedDenial((deniedToken) => {
    if (deniedToken !== token) return;
    revokedRef.current = true;
    bumpGeneration();
    setRevoked(true);
  }), [bumpGeneration, token]);
  useEffect(() => {
    if (denied(query.error)) denyCannedScope(client, principal, denialMessage(query.error));
  }, [client, principal, query.error]);
  function commit(stamp: number, startedToken: string, next: CannedSignature): boolean {
    if (closedRef.current || revokedRef.current) return false;
    if (stamp !== generation.current || startedToken !== token) return false;
    client.setQueryData(key, next);
    return true;
  }
  if (revoked) return null;
  return (
    <section aria-label="Assinatura do atendente">
      <h3>Assinatura</h3>
      <p>O servidor acrescenta o prefixo uma vez, na aceitação da mensagem humana. O rascunho não deve repetir esse prefixo.</p>
      {query.isPending ? <p>Carregando assinatura…</p> : null}
      {query.isError ? <p>Não foi possível carregar a assinatura.</p> : null}
      {query.data ? (
        <SignatureForm
          key={`${query.data.version}:${query.data.enabled}:${query.data.prefix}`}
          initial={query.data}
          writable={writable}
          stamp={stamp}
          token={token}
          onDenied={onDenied}
          commit={commit}
        />
      ) : null}
    </section>
  );
}

function SignatureForm({
  initial,
  writable,
  stamp,
  token,
  commit,
  onDenied,
}: {
  initial: CannedSignature;
  writable: boolean;
  stamp: number;
  token: string;
  commit: (stamp: number, token: string, next: CannedSignature) => boolean;
  onDenied: (error: unknown) => void;
}) {
  const [enabled, setEnabled] = useState(initial.enabled);
  const [prefix, setPrefix] = useState(initial.prefix);
  const [version, setVersion] = useState(initial.version);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function submit() {
    const error = prefixError(prefix);
    if (error) {
      setMessage(error);
      return;
    }
    const started = { stamp, token };
    setBusy(true);
    setMessage("");
    try {
      const saved = await saveSignature({ enabled, prefix, version });
      if (!commit(started.stamp, started.token, saved)) return;
      setVersion(saved.version);
      setEnabled(saved.enabled);
      setPrefix(saved.prefix);
      setMessage("Assinatura gravada.");
    } catch (error) {
      if (denied(error)) {
        onDenied(error);
        return;
      }
      setMessage(error instanceof PanelRequestError ? error.message : "Não foi possível gravar a assinatura.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <label>
        <input type="checkbox" checked={enabled} disabled={!writable} onChange={(event) => setEnabled(event.target.checked)} />
        Aplicar prefixo na aceitação
      </label>
      <label>
        Prefixo
        <input value={prefix} disabled={!writable} onChange={(event) => setPrefix(event.target.value)} />
      </label>
      <p>Versão {version}. A variável da assinatura é somente nome_atendente.</p>
      {writable ? <button type="submit" disabled={busy}>Gravar assinatura</button> : <p>Seu papel não altera a assinatura.</p>}
      {message ? <p>{message}</p> : null}
    </form>
  );
}
