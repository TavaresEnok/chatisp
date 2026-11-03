import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { listTemplates, queueTemplate, recordConsent, syncTemplates, type ChannelTemplate, type Outbound, type TemplateCommand } from "@/api/channelpolicy";
import { PanelRequestError } from "@/api/problems";
import { denyPolicyScope, forgetPolicyQueries, policyKey, policyScopeToken, subscribePolicyDenial } from "@/features/channelpolicy/cache";
import { isOfficialCloud, templateCanQueue, windowIndicator } from "@/features/channelpolicy/policy";
import { readTemplateIntent, releaseTemplateIntent, subscribeTemplateIntents, templateIntentKey, writeTemplateIntent } from "@/features/channelpolicy/template-intent";
import type { PanelPrincipal } from "@/stores/session-store";

function denied(error: unknown): boolean {
  return error instanceof PanelRequestError && [403, 404].includes(error.status);
}

export function WindowIndicator({ connectorType }: { connectorType: string }) {
  if (windowIndicator(connectorType) === "hidden") return null;
  return (
    <p role="status">
      Janela oficial de 24 horas: indisponível neste painel. O servidor revalida a janela no envio. Este indicador não calcula o prazo no navegador.
    </p>
  );
}

export function TemplateCatalog({
  principal,
  inboxId,
  conversationId,
  connectorType,
  version,
}: {
  principal: PanelPrincipal;
  inboxId: string;
  conversationId: string;
  connectorType: string;
  version: number | null;
}) {
  const client = useQueryClient();
  const official = isOfficialCloud(connectorType);
  const key = policyKey(principal, inboxId, "templates");
  const token = policyScopeToken(principal, inboxId);
  const revokedRef = useRef(false);
  const generation = useRef(0);
  const [revoked, setRevoked] = useState(false);
  const [denial, setDenial] = useState("");
  const query = useQuery({
    queryKey: key,
    queryFn: async ({ signal }) => {
      if (revokedRef.current) throw new DOMException("A consulta foi invalidada.", "AbortError");
      const stamp = generation.current;
      const templates = await listTemplates(inboxId, signal);
      if (revokedRef.current || stamp !== generation.current || signal.aborted) {
        throw new DOMException("A consulta foi invalidada.", "AbortError");
      }
      return templates;
    },
    enabled: official && !revoked,
    retry: false,
  });
  useEffect(() => () => {
    generation.current += 1;
    forgetPolicyQueries(client, principal, inboxId);
  }, [client, principal, inboxId]);
  useEffect(() => subscribePolicyDenial((deniedToken, message) => {
    if (deniedToken !== token) return;
    revokedRef.current = true;
    generation.current += 1;
    setRevoked(true);
    setDenial(message);
  }), [token]);
  useLayoutEffect(() => {
    if (!denied(query.error) || revokedRef.current) return;
    denyPolicyScope(client, principal, inboxId, query.error instanceof PanelRequestError ? query.error.message : "Acesso negado.");
  }, [client, principal, inboxId, query.error]);
  function onDenied(error: unknown) {
    denyPolicyScope(client, principal, inboxId, error instanceof PanelRequestError ? error.message : "Acesso negado.");
  }
  if (!official) return null;
  if (revoked || denied(query.error)) {
    const message = denial || (query.error instanceof PanelRequestError ? query.error.message : "Acesso negado.");
    return (
      <section aria-label="Templates da caixa">
        <h2>Templates aprovados</h2>
        <p>{message}</p>
      </section>
    );
  }
  return (
    <section aria-label="Templates da caixa">
      <h2>Templates aprovados</h2>
      <p>A lista vem do catálogo gravado para a WABA desta caixa. Fonte que não seja driver não entra na fila. O corpo visível da mensagem fica sendo o identificador do template.</p>
      {principal.role === "admin" ? <SyncButton inboxId={inboxId} onDone={() => { if (!revokedRef.current) void client.invalidateQueries({ queryKey: key }); }} onDenied={onDenied} /> : <p>Sincronizar o catálogo é restrito ao administrador.</p>}
      {query.isPending ? <p>Carregando templates…</p> : null}
      {query.isError ? <p>Não foi possível carregar os templates.</p> : null}
      <ul>
        {(query.data ?? []).map((template) => (
          <li key={`${template.external_id}:${template.language}`}>
            <TemplateRow principal={principal} inboxId={inboxId} conversationId={conversationId} template={template} version={version} onSend={(body) => queueTemplate(inboxId, conversationId, body)} onDenied={onDenied} />
          </li>
        ))}
      </ul>
    </section>
  );
}

function SyncButton({ inboxId, onDone, onDenied }: { inboxId: string; onDone: () => void; onDenied: (error: unknown) => void }) {
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  async function sync() {
    setBusy(true);
    setMessage("");
    try {
      await syncTemplates(inboxId);
      onDone();
      setMessage("Pedido de sincronização concluído. A lista mostra o catálogo gravado.");
    } catch (error) {
      if (denied(error)) {
        onDenied(error);
        return;
      }
      setMessage(error instanceof PanelRequestError ? error.message : "Não foi possível sincronizar.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div>
      <button type="button" disabled={busy} onClick={() => void sync()}>Sincronizar catálogo</button>
      {message ? <p>{message}</p> : null}
    </div>
  );
}

function ambiguousTemplateResult(error: unknown): boolean {
  if (!(error instanceof PanelRequestError)) return true;
  return error.status === 0 || error.status === 408 || error.status === 429 || error.status >= 500;
}

const confirmedTemplateStates = new Set(["queued", "sending", "retrying", "accepted", "sent"]);

function templateDisposition(result: Outbound): "confirmed" | "failed" | "cancelled" | "uncertain" | "unknown" {
  if (result.state === "failed") return "failed";
  if (result.state === "cancelled") return "cancelled";
  if (result.state === "uncertain") return "uncertain";
  if (confirmedTemplateStates.has(result.state)) return "confirmed";
  return "unknown";
}

function TemplateRow({
  principal,
  inboxId,
  conversationId,
  template,
  version,
  onSend,
  onDenied,
}: {
  principal: PanelPrincipal;
  inboxId: string;
  conversationId: string;
  template: ChannelTemplate;
  version: number | null;
  onSend: (body: TemplateCommand) => Promise<Outbound>;
  onDenied: (error: unknown) => void;
}) {
  const intentKey = templateIntentKey({ ...principal, inboxId, conversationId, templateId: template.external_id, language: template.language });
  const [purpose, setPurpose] = useState(() => readTemplateIntent(intentKey)?.purpose ?? "");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [unresolved, setUnresolved] = useState(() => readTemplateIntent(intentKey) !== null);
  const mounted = useRef(true);
  const allowed = templateCanQueue(template);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => subscribeTemplateIntents(() => {
    if (mounted.current) setUnresolved(readTemplateIntent(intentKey) !== null);
  }), [intentKey]);
  async function submit(forceNew: boolean) {
    const trimmed = purpose.trim();
    if (!allowed || busy) return;
    let payload = forceNew ? null : readTemplateIntent(intentKey);
    if (!forceNew && payload) {
      if (trimmed.length > 0 && trimmed !== payload.purpose) {
        setMessage("Há um envio sem confirmação. Repita a finalidade original para reutilizar a chave ou escolha novo envio do template.");
        return;
      }
    } else {
      if (version === null || trimmed.length === 0) return;
      payload = {
        client_request_id: crypto.randomUUID(),
        expected_version: version,
        language: template.language,
        purpose: trimmed,
        template_id: template.external_id,
      };
      writeTemplateIntent(intentKey, payload);
      setUnresolved(true);
    }
    if (!payload) return;
    setBusy(true);
    setMessage("");
    try {
      const result = await onSend(payload);
      if (!mounted.current || readTemplateIntent(intentKey)?.client_request_id !== payload.client_request_id) return;
      const disposition = templateDisposition(result);
      if (disposition === "uncertain" || disposition === "unknown") {
        setUnresolved(true);
        setMessage(disposition === "uncertain"
          ? "O resultado do template é incerto. A chave e o payload originais continuam válidos."
          : "O estado do template é desconhecido. A chave e o payload originais continuam válidos.");
        return;
      }
      const released = releaseTemplateIntent(intentKey, payload.client_request_id);
      if (!mounted.current || !released) return;
      setUnresolved(false);
      if (disposition === "failed") {
        setMessage("O envio do template falhou.");
        return;
      }
      if (disposition === "cancelled") {
        setMessage("O envio do template foi cancelado.");
        return;
      }
      setMessage("Template colocado na fila existente.");
    } catch (error) {
      if (ambiguousTemplateResult(error)) {
        if (!mounted.current || readTemplateIntent(intentKey)?.client_request_id !== payload.client_request_id) return;
        setUnresolved(true);
        setMessage(error instanceof PanelRequestError ? `${error.message} A chave e o payload originais continuam válidos.` : "A resposta não chegou. Repita a mesma finalidade para reutilizar a chave.");
        return;
      }
      if (denied(error)) {
        onDenied(error);
        return;
      }
      const released = releaseTemplateIntent(intentKey, payload.client_request_id);
      if (!mounted.current || !released) return;
      setUnresolved(false);
      setMessage(error instanceof PanelRequestError ? error.message : "O envio do template foi recusado.");
    } finally {
      if (mounted.current) setBusy(false);
    }
  }
  return (
    <article>
      <p>{template.name} · {template.language} · {template.state} · fonte {template.source}</p>
      {allowed ? (
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void submit(false);
          }}
        >
          <label>
            Finalidade do template
            <input value={purpose} onChange={(event) => setPurpose(event.target.value)} />
          </label>
          <button type="submit" disabled={busy || (!unresolved && (version === null || purpose.trim().length === 0))}>Colocar template na fila</button>
          {unresolved ? <button type="button" disabled={busy || version === null || purpose.trim().length === 0} onClick={() => void submit(true)}>Novo envio do template</button> : null}
          {version === null ? <p>A versão da conversa ainda não chegou.</p> : null}
        </form>
      ) : (
        <p>Este template não está aprovado por driver e não pode entrar na fila.</p>
      )}
      {message ? <p>{message}</p> : null}
    </article>
  );
}

export function ConsentForm({ principal, inboxId, contactId }: { principal: PanelPrincipal; inboxId: string; contactId: string }) {
  const client = useQueryClient();
  const token = policyScopeToken(principal, inboxId);
  const revokedRef = useRef(false);
  const [revoked, setRevoked] = useState(false);
  const [denial, setDenial] = useState("");
  const [purpose, setPurpose] = useState("");
  const [source, setSource] = useState("");
  const [evidence, setEvidence] = useState("");
  const [revoke, setRevoke] = useState(false);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => subscribePolicyDenial((deniedToken, text) => {
    if (deniedToken !== token) return;
    revokedRef.current = true;
    setRevoked(true);
    setDenial(text);
  }), [token]);
  async function submit() {
    if (busy || revokedRef.current) return;
    setBusy(true);
    setMessage("");
    try {
      const result = await recordConsent(inboxId, contactId, { purpose: purpose.trim(), source: source.trim(), evidence: evidence.trim(), revoke });
      if (revokedRef.current) return;
      const ids = result.invalidated_ids.join(", ");
      setMessage(revoke ? `Revogação registrada. Pendências invalidadas: ${ids || "nenhuma"}.` : "Concessão registrada. O painel não guarda um estado local de consentimento.");
    } catch (error) {
      if (denied(error)) {
        denyPolicyScope(client, principal, inboxId, error instanceof PanelRequestError ? error.message : "Acesso negado.");
        return;
      }
      if (revokedRef.current) return;
      setMessage(error instanceof PanelRequestError ? error.message : "Não foi possível registrar o consentimento.");
    } finally {
      if (!revokedRef.current) setBusy(false);
    }
  }
  if (revoked) {
    return (
      <form aria-label="Consentimento do contato">
        <h2>Consentimento</h2>
        <p>{denial}</p>
      </form>
    );
  }
  return (
    <form
      aria-label="Consentimento do contato"
      onSubmit={(event) => {
        event.preventDefault();
        void submit();
      }}
    >
      <h2>Consentimento</h2>
      <p>Cada envio grava um evento novo. Não há consulta do estado atual nesta tela.</p>
      <label>Finalidade do consentimento <input value={purpose} onChange={(event) => setPurpose(event.target.value)} /></label>
      <label>Origem <input value={source} onChange={(event) => setSource(event.target.value)} /></label>
      <label>Evidência <input value={evidence} onChange={(event) => setEvidence(event.target.value)} /></label>
      <label><input type="checkbox" checked={revoke} onChange={(event) => setRevoke(event.target.checked)} /> Revogar</label>
      <button type="submit" disabled={busy || purpose.trim().length === 0 || source.trim().length === 0 || evidence.trim().length === 0}>Registrar consentimento</button>
      {message ? <p>{message}</p> : null}
    </form>
  );
}
