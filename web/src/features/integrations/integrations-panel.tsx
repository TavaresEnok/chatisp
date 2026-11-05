import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  API_SCOPES,
  WEBHOOK_EVENTS,
  createApiToken,
  createWebhook,
  disableWebhook,
  listApiTokens,
  listWebhookDeliveries,
  listWebhooks,
  revokeApiToken,
  type ApiToken,
  type WebhookDelivery,
  type WebhookSubscription,
} from "@/api/integrations";
import { PanelRequestError, errorMessage } from "@/api/problems";
import {
  canManageIntegrations,
  deliveryStatusLabel,
  eventLabel,
  latestDelivery,
  orderedEvents,
  orderedScopes,
  parseRequestLimit,
  scopeLabel,
  webhookSubmitMessage,
} from "@/features/integrations/rules";
import type { PanelPrincipal } from "@/stores/session-store";
import { CustomerLinkSettings } from "@/features/erp/customer-link-settings";

export function IntegrationsPanel({ principal }: { principal: PanelPrincipal }) {
  if (!canManageIntegrations(principal)) {
    return (
      <section aria-label="Integrações" className="flex flex-col gap-2">
        <h2 className="text-base font-medium">Integrações</h2>
        <p role="status">Sem acesso às integrações.</p>
      </section>
    );
  }
  return <IntegrationsAdmin sessionId={principal.sessionId} />;
}

function IntegrationsAdmin({ sessionId }: { sessionId: string }) {
  return (
    <section aria-label="Integrações" className="flex max-w-3xl flex-col gap-6">
      <header className="flex flex-col gap-2">
        <h2 className="text-base font-medium">Integrações</h2>
        <p className="text-sm text-muted-foreground">
          Tokens e webhooks de saída do provedor. O segredo de cada um aparece uma única vez.
        </p>
        <a href="/api/public/openapi.json">Documentação OpenAPI pública</a>
      </header>
      <TokenSection sessionId={sessionId} />
      <WebhookSection sessionId={sessionId} />
      <ErpConnector sessionId={sessionId} />
      <EvolutionConnector />
    </section>
  );
}

function TokenSection({ sessionId }: { sessionId: string }) {
  const client = useQueryClient();
  const queryKey = ["integrations", sessionId, "tokens"] as const;
  const query = useQuery({ queryKey, queryFn: ({ signal }) => listApiTokens(signal), retry: false });
  const [scopes, setScopes] = useState<string[]>([]);
  const [limit, setLimit] = useState("60");
  const [secret, setSecret] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => {
      const requestLimit = parseRequestLimit(limit);
      if (scopes.length === 0 || requestLimit === null) {
        return Promise.reject(new PanelRequestError("Limite inválido.", 422));
      }
      return createApiToken({ scopes: orderedScopes(scopes), request_limit: requestLimit });
    },
    onSuccess: async (issued) => {
      setSecret(issued.secret.length > 0 ? issued.secret : null);
      setScopes([]);
      await client.invalidateQueries({ queryKey });
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) => revokeApiToken(id),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey });
    },
  });

  function toggleScope(scope: string) {
    setScopes((current) => (current.includes(scope) ? current.filter((item) => item !== scope) : [...current, scope]));
  }

  return (
    <section aria-label="Tokens de API" className="flex flex-col gap-3">
      <h3 className="text-sm font-medium">Tokens de API</h3>
      <form
        className="flex flex-col gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          setSecret(null);
          create.mutate();
        }}
      >
        <fieldset className="flex flex-col gap-1">
          <legend className="text-sm">Escopos</legend>
          {API_SCOPES.map((scope) => (
            <label key={scope} className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={scopes.includes(scope)} onChange={() => toggleScope(scope)} />
              {scopeLabel(scope)}
            </label>
          ))}
        </fieldset>
        <label className="flex flex-col gap-1 text-sm">
          Limite por minuto
          <input
            aria-label="Limite por minuto"
            inputMode="numeric"
            value={limit}
            onChange={(event) => setLimit(event.target.value)}
          />
        </label>
        <button type="submit" disabled={create.isPending || scopes.length === 0 || parseRequestLimit(limit) === null}>
          Gerar token
        </button>
      </form>
      {create.isError ? <p role="alert">{errorMessage(create.error)}</p> : null}
      {secret ? (
        <SecretOnce
          label="Segredo do token"
          value={secret}
          copyLabel="Copiar segredo"
          hideLabel="Ocultar segredo do token"
          onHide={() => setSecret(null)}
        />
      ) : null}
      {query.isPending ? <p role="status">Carregando tokens…</p> : null}
      {query.isError ? <p role="alert">{errorMessage(query.error)}</p> : null}
      {query.data ? <TokenList tokens={query.data} pendingId={revoke.isPending ? revoke.variables : undefined} onRevoke={(id) => revoke.mutate(id)} /> : null}
      {revoke.isError ? <p role="alert">{errorMessage(revoke.error)}</p> : null}
    </section>
  );
}

function TokenList({
  tokens,
  pendingId,
  onRevoke,
}: {
  tokens: ApiToken[];
  pendingId: string | undefined;
  onRevoke: (id: string) => void;
}) {
  if (tokens.length === 0) return <p>Nenhum token de API.</p>;
  return (
    <ul aria-label="Tokens de API" className="flex flex-col gap-2">
      {tokens.map((token) => (
        <li key={token.id} className="rounded-xl bg-card p-3 text-sm ring-1 ring-foreground/10">
          <p>{token.id}</p>
          <p>{orderedScopes(token.scopes).map((scope) => scopeLabel(scope)).join(", ")}</p>
          <p>Limite {token.request_limit} por minuto</p>
          <p>{token.revoked ? "revogado" : "ativo"}</p>
          {token.revoked ? null : (
            <button type="button" disabled={pendingId === token.id} onClick={() => onRevoke(token.id)}>
              Revogar
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}

function WebhookSection({ sessionId }: { sessionId: string }) {
  const client = useQueryClient();
  const queryKey = ["integrations", sessionId, "webhooks"] as const;
  const query = useQuery({ queryKey, queryFn: ({ signal }) => listWebhooks(signal), retry: false });
  const [events, setEvents] = useState<string[]>([]);
  const [url, setUrl] = useState("");
  const [secret, setSecret] = useState<string | null>(null);
  const create = useMutation({
    mutationFn: () => createWebhook({ url: url.trim(), events: orderedEvents(events) }),
    onSuccess: async (issued) => {
      setSecret(issued.signing_secret.length > 0 ? issued.signing_secret : null);
      setUrl("");
      setEvents([]);
      await client.invalidateQueries({ queryKey });
    },
  });
  const disable = useMutation({
    mutationFn: (id: string) => disableWebhook(id),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey });
    },
  });
  const probe = useMutation({
    mutationFn: (id: string) => listWebhookDeliveries(id),
  });

  function toggleEvent(event: string) {
    setEvents((current) => (current.includes(event) ? current.filter((item) => item !== event) : [...current, event]));
  }

  return (
    <section aria-label="Assinaturas de webhook" className="flex flex-col gap-3">
      <h3 className="text-sm font-medium">Webhooks de saída</h3>
      <form
        className="flex flex-col gap-3"
        onSubmit={(event) => {
          event.preventDefault();
          setSecret(null);
          create.mutate();
        }}
      >
        <label className="flex flex-col gap-1 text-sm">
          URL do webhook
          <input aria-label="URL do webhook" value={url} onChange={(event) => setUrl(event.target.value)} />
        </label>
        <fieldset className="flex flex-col gap-1">
          <legend className="text-sm">Eventos</legend>
          {WEBHOOK_EVENTS.map((event) => (
            <label key={event} className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={events.includes(event)} onChange={() => toggleEvent(event)} />
              {eventLabel(event)}
            </label>
          ))}
        </fieldset>
        <button type="submit" disabled={create.isPending || events.length === 0 || url.trim() === ""}>
          Cadastrar assinatura
        </button>
      </form>
      {create.isError ? <p role="alert">{webhookSubmitMessage(create.error)}</p> : null}
      {secret ? (
        <SecretOnce
          label="Segredo do webhook"
          value={secret}
          copyLabel="Copiar segredo do webhook"
          hideLabel="Ocultar segredo do webhook"
          onHide={() => setSecret(null)}
        />
      ) : null}
      {query.isPending ? <p role="status">Carregando webhooks…</p> : null}
      {query.isError ? <p role="alert">{errorMessage(query.error)}</p> : null}
      {query.data ? (
        <WebhookList
          subscriptions={query.data}
          disablingId={disable.isPending ? disable.variables : undefined}
          probingId={probe.isPending ? probe.variables : undefined}
          probedId={probe.variables}
          deliveries={probe.data}
          probeError={probe.isError ? webhookSubmitMessage(probe.error) : null}
          onDisable={(id) => disable.mutate(id)}
          onProbe={(id) => probe.mutate(id)}
        />
      ) : null}
      {disable.isError ? <p role="alert">{errorMessage(disable.error)}</p> : null}
    </section>
  );
}

function WebhookList({
  subscriptions,
  disablingId,
  probingId,
  probedId,
  deliveries,
  probeError,
  onDisable,
  onProbe,
}: {
  subscriptions: WebhookSubscription[];
  disablingId: string | undefined;
  probingId: string | undefined;
  probedId: string | undefined;
  deliveries: WebhookDelivery[] | undefined;
  probeError: string | null;
  onDisable: (id: string) => void;
  onProbe: (id: string) => void;
}) {
  if (subscriptions.length === 0) return <p>Nenhuma assinatura de webhook.</p>;
  return (
    <ul aria-label="Assinaturas de webhook" className="flex flex-col gap-2">
      {subscriptions.map((subscription) => (
        <li key={subscription.id} className="rounded-xl bg-card p-3 text-sm ring-1 ring-foreground/10">
          <p>{subscription.url}</p>
          <p>{orderedEvents(subscription.events).map((event) => eventLabel(event)).join(", ")}</p>
          <p>{subscription.enabled ? "ativa" : "desativada"}</p>
          <div className="mt-2 flex gap-2">
            {subscription.enabled ? (
              <button type="button" disabled={disablingId === subscription.id} onClick={() => onDisable(subscription.id)}>
                Desativar
              </button>
            ) : null}
            <button type="button" disabled={probingId === subscription.id} onClick={() => onProbe(subscription.id)}>
              Testar entrega
            </button>
          </div>
          {probedId === subscription.id && probeError ? <p role="alert">{probeError}</p> : null}
          {probedId === subscription.id && deliveries ? <DeliveryStatus delivery={latestDelivery(deliveries)} /> : null}
        </li>
      ))}
    </ul>
  );
}

function DeliveryStatus({ delivery }: { delivery: WebhookDelivery | null }) {
  if (!delivery) return <p>Nenhuma entrega ainda.</p>;
  const label = deliveryStatusLabel(delivery.status);
  return (
    <p>
      Status da última entrega: {label} ({delivery.status}), tentativa {delivery.attempt}
    </p>
  );
}

function EvolutionConnector() {
  return (
    <section aria-label="Conector Evolution" className="rounded-xl bg-card p-4 text-sm ring-1 ring-foreground/10">
      <h3 className="text-sm font-medium">Conector Evolution</h3>
      <p>
        A modalidade Web permanece desligada. Não há QR, sessão gravada nem chamada ao serviço.
        A decisão D2-04 continua em aberto.
      </p>
      <dl className="mt-2">
        <div>
          <dt>Situação</dt>
          <dd>desligada</dd>
        </div>
        <div>
          <dt>QR</dt>
          <dd>não exibido</dd>
        </div>
      </dl>
    </section>
  );
}

function ErpConnector({ sessionId }: { sessionId: string }) {
  return (
    <section aria-label="Conector ERP" className="rounded-xl bg-card p-4 text-sm ring-1 ring-foreground/10">
      <h3 className="text-sm font-medium">Conector ERP</h3>
      <p>Item 11-conector-erp. O conector está simulado e desligado. O driver é fake e não há chamada a um ERP real.</p>
      <dl className="mt-2">
        <div>
          <dt>Situação</dt>
          <dd>simulado</dd>
        </div>
        <div>
          <dt>Estado</dt>
          <dd>desligado</dd>
        </div>
      </dl>
      <CustomerLinkSettings scope={sessionId} />
    </section>
  );
}

function SecretOnce({
  label,
  value,
  copyLabel,
  hideLabel,
  onHide,
}: {
  label: string;
  value: string;
  copyLabel: string;
  hideLabel: string;
  onHide: () => void;
}) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setFailed(false);
    } catch {
      setCopied(false);
      setFailed(true);
    }
  }

  return (
    <div className="flex flex-col gap-2 rounded-xl bg-card p-3 ring-1 ring-foreground/10">
      <p>Este segredo aparece uma única vez. Copie agora. A lista não volta a mostrá-lo.</p>
      <label className="flex flex-col gap-1 text-sm">
        {label}
        <input aria-label={label} readOnly spellCheck={false} autoComplete="off" value={value} />
      </label>
      <div className="flex gap-2">
        <button type="button" onClick={() => void copy()}>
          {copyLabel}
        </button>
        <button type="button" onClick={onHide}>
          {hideLabel}
        </button>
      </div>
      {copied ? <p role="status">Segredo copiado.</p> : null}
      {failed ? <p role="alert">Não foi possível copiar. Selecione o campo.</p> : null}
    </div>
  );
}
