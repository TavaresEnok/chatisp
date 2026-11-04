-- +goose Up
-- M6: one simulated ERP connection per tenant and durable outbound webhooks.
-- The driver column accepts only the identified fake. No supplier endpoint is stored.
-- Financial amounts are not columns. Webhook bodies stay outside this schema.

CREATE TABLE erp_connections (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    id uuid NOT NULL,
    driver text NOT NULL CHECK (driver = 'fake'),
    simulated boolean NOT NULL CHECK (simulated),
    enabled boolean NOT NULL DEFAULT false,
    availability text NOT NULL DEFAULT 'up' CHECK (availability IN ('up', 'down')),
    sector_id uuid,
    capabilities text[] NOT NULL DEFAULT ARRAY['SearchCustomer', 'GetCustomerSummary']::text[],
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id),
    FOREIGN KEY (tenant_id, sector_id) REFERENCES sectors (tenant_id, id),
    CONSTRAINT erp_connections_capabilities_chk CHECK (
        capabilities <@ ARRAY[
            'SearchCustomer',
            'GetCustomerSummary',
            'ListOpenInvoices',
            'GetInvoiceDocument',
            'GetPixCode'
        ]::text[]
    )
);

CREATE TABLE erp_links (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    id uuid NOT NULL,
    contact_id uuid NOT NULL,
    conversation_id uuid,
    external_customer_id text NOT NULL,
    external_contract_id text,
    external_protocol text,
    selected boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, contact_id) REFERENCES contacts (tenant_id, id),
    FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    CONSTRAINT erp_links_customer_chk CHECK (
        char_length(external_customer_id) BETWEEN 1 AND 128
        AND external_customer_id = btrim(external_customer_id)
        AND external_customer_id !~ '[[:space:]]'
    ),
    CONSTRAINT erp_links_contract_chk CHECK (
        external_contract_id IS NULL OR (
            char_length(external_contract_id) BETWEEN 1 AND 128
            AND external_contract_id = btrim(external_contract_id)
            AND external_contract_id !~ '[[:space:]]'
        )
    ),
    CONSTRAINT erp_links_protocol_chk CHECK (
        external_protocol IS NULL OR (
            char_length(external_protocol) BETWEEN 1 AND 128
            AND external_protocol = btrim(external_protocol)
        )
    )
);

CREATE UNIQUE INDEX erp_links_one_selected_conversation
    ON erp_links (tenant_id, conversation_id)
    WHERE selected AND conversation_id IS NOT NULL;

CREATE TABLE erp_actions (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    id uuid NOT NULL,
    connection_id uuid NOT NULL,
    conversation_id uuid,
    capability text NOT NULL,
    local_key text NOT NULL,
    request_hash bytea NOT NULL,
    effect text NOT NULL CHECK (effect IN ('read', 'unclassified')),
    outcome text NOT NULL CHECK (outcome IN ('ok', 'unavailable', 'hidden', 'uncertain', 'not_found', 'denied')),
    external_ref text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, local_key),
    FOREIGN KEY (tenant_id, connection_id) REFERENCES erp_connections (tenant_id, id),
    FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    CONSTRAINT erp_actions_capability_chk CHECK (
        capability IN (
            'SearchCustomer',
            'GetCustomerSummary',
            'ListOpenInvoices',
            'GetInvoiceDocument',
            'GetPixCode'
        )
    ),
    CONSTRAINT erp_actions_key_chk CHECK (
        char_length(local_key) BETWEEN 1 AND 200
        AND local_key = btrim(local_key)
    ),
    CONSTRAINT erp_actions_hash_chk CHECK (octet_length(request_hash) = 32),
    CONSTRAINT erp_actions_ref_chk CHECK (
        external_ref IS NULL OR (
            char_length(external_ref) BETWEEN 1 AND 128
            AND external_ref = btrim(external_ref)
            AND external_ref !~ '[[:space:]]'
        )
    )
);

CREATE TABLE webhook_subscriptions (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    id uuid NOT NULL,
    url text NOT NULL,
    secret_ciphertext bytea NOT NULL,
    secret_key_version text NOT NULL,
    secret_aad text NOT NULL,
    events text[] NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT webhook_subscriptions_url_chk CHECK (
        char_length(url) BETWEEN 8 AND 2048
        AND url = btrim(url)
        AND url !~ '[[:space:]]'
    ),
    CONSTRAINT webhook_subscriptions_secret_chk CHECK (
        octet_length(secret_ciphertext) BETWEEN 1 AND 4096
        AND char_length(secret_key_version) BETWEEN 1 AND 32
        AND char_length(secret_aad) BETWEEN 1 AND 800
    ),
    CONSTRAINT webhook_subscriptions_events_chk CHECK (
        cardinality(events) >= 1
        AND events <@ ARRAY[
            'conversation.created',
            'conversation.assigned',
            'conversation.closed',
            'message.created'
        ]::text[]
    )
);

CREATE TABLE webhook_deliveries (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    id uuid NOT NULL,
    subscription_id uuid NOT NULL,
    event_type text NOT NULL,
    source_id uuid NOT NULL,
    body bytea NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'delivered', 'dead')),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0 AND attempt_count <= 5),
    next_attempt_at timestamptz NOT NULL,
    occurred_at timestamptz NOT NULL,
    last_status integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    UNIQUE (tenant_id, subscription_id, event_type, source_id),
    FOREIGN KEY (tenant_id, subscription_id) REFERENCES webhook_subscriptions (tenant_id, id),
    CONSTRAINT webhook_deliveries_type_chk CHECK (
        event_type IN (
            'conversation.created',
            'conversation.assigned',
            'conversation.closed',
            'message.created'
        )
    ),
    CONSTRAINT webhook_deliveries_body_chk CHECK (octet_length(body) BETWEEN 1 AND 65536)
);

CREATE INDEX webhook_deliveries_pending_idx
    ON webhook_deliveries (tenant_id, next_attempt_at, id)
    WHERE status = 'pending';

ALTER TABLE conversation_events DROP CONSTRAINT conversation_events_kind_chk;
ALTER TABLE conversation_events ADD CONSTRAINT conversation_events_kind_chk CHECK (
    kind IN (
        'assumed', 'controller_changed', 'closed', 'reopened',
        'wait_started', 'wait_ended', 'customer_message', 'outbound_queued',
        'snoozed', 'snooze_cleared', 'erp_action'
    )
    OR kind ~ '^(transferred_member|transferred_sector|unassigned|distributed|snooze_return|offline_unassigned):[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}$'
);

ALTER TABLE erp_connections ENABLE ROW LEVEL SECURITY;
ALTER TABLE erp_connections FORCE ROW LEVEL SECURITY;
ALTER TABLE erp_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE erp_links FORCE ROW LEVEL SECURITY;
ALTER TABLE erp_actions ENABLE ROW LEVEL SECURITY;
ALTER TABLE erp_actions FORCE ROW LEVEL SECURITY;
ALTER TABLE webhook_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_subscriptions FORCE ROW LEVEL SECURITY;
ALTER TABLE webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_deliveries FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON erp_connections TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON erp_links TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON erp_actions TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON webhook_subscriptions TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON webhook_deliveries TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE ON erp_connections TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON erp_links TO chatisp_app;
GRANT SELECT, INSERT ON erp_actions TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON webhook_subscriptions TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON webhook_deliveries TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'rollback descartaria o contrato do ERP simulado e as entregas de webhook'; END $$;
-- +goose StatementEnd
