-- +goose Up
-- Explicit permission for sensitive ERP reads, unlink of a contact binding,
-- and the durable result used by replay. No supplier endpoint and no monetary amount is stored.

GRANT DELETE ON erp_links TO chatisp_app;

CREATE TABLE erp_capability_grants (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    membership_id uuid NOT NULL,
    capability text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, membership_id, capability),
    FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships (tenant_id, id),
    CONSTRAINT erp_capability_grants_name_chk CHECK (
        capability IN ('ListOpenInvoices', 'GetInvoiceDocument', 'GetPixCode')
    )
);

ALTER TABLE erp_capability_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE erp_capability_grants FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON erp_capability_grants TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, DELETE ON erp_capability_grants TO chatisp_app;

-- Durable replay of the allowed result, the outbox pair of a second copy,
-- and a timeline kind that already exposes operation, outcome and reason.
ALTER TABLE erp_actions
    ADD COLUMN reason text,
    ADD COLUMN result jsonb,
    ADD COLUMN client_request_id text,
    ADD COLUMN outbox_operation text,
    ADD COLUMN principal_id uuid,
    ADD COLUMN message_id uuid,
    ADD COLUMN attempt_id uuid;

ALTER TABLE erp_actions
    ADD CONSTRAINT erp_actions_reason_chk CHECK (
        reason IS NULL OR reason IN (
            'ok', 'timeout', 'circuit_open', 'down', 'grant', 'sector', 'choice', 'missing',
            'local_key', 'outbox', 'invoice', 'not_found', 'hidden', 'unavailable', 'uncertain',
            'dispatch', 'sent', 'linked', 'unlinked', 'protocol', 'invalid'
        )
    ),
    ADD CONSTRAINT erp_actions_result_chk CHECK (
        result IS NULL OR (jsonb_typeof(result) = 'object' AND octet_length(result::text) <= 8192)
    ),
    ADD CONSTRAINT erp_actions_request_chk CHECK (
        client_request_id IS NULL OR (
            char_length(client_request_id) BETWEEN 1 AND 200
            AND client_request_id = btrim(client_request_id)
        )
    ),
    ADD CONSTRAINT erp_actions_outbox_chk CHECK (
        outbox_operation IS NULL OR outbox_operation = 'erp_second_copy'
    ),
    ADD CONSTRAINT erp_actions_pair_chk CHECK (
        (message_id IS NULL) = (attempt_id IS NULL)
        AND (message_id IS NULL OR (principal_id IS NOT NULL AND client_request_id IS NOT NULL AND outbox_operation IS NOT NULL))
    );

ALTER TABLE conversation_events DROP CONSTRAINT conversation_events_kind_chk;
ALTER TABLE conversation_events ADD CONSTRAINT conversation_events_kind_chk CHECK (
    kind IN (
        'assumed', 'controller_changed', 'closed', 'reopened',
        'wait_started', 'wait_ended', 'customer_message', 'outbound_queued',
        'snoozed', 'snooze_cleared', 'erp_action'
    )
    OR kind ~ '^(transferred_member|transferred_sector|unassigned|distributed|snooze_return|offline_unassigned):[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}$'
    OR kind ~ '^erp_action:(SearchCustomer|GetCustomerSummary|ListOpenInvoices|GetInvoiceDocument|GetPixCode|link|unlink|protocol):(ok|unavailable|hidden|uncertain|not_found|denied|conflict):(ok|timeout|circuit_open|down|grant|sector|choice|missing|local_key|outbox|invoice|not_found|hidden|unavailable|uncertain|dispatch|sent|linked|unlinked|protocol|invalid)$'
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'rollback descartaria as permissões explícitas do ERP simulado'; END $$;
-- +goose StatementEnd
