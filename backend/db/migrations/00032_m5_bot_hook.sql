-- +goose Up
-- Um gancho simulado por caixa. O segredo do token não fica nesta tabela:
-- só o hash e um prefixo curto. HTTP 200 do gancho é o recibo `received`,
-- que não encerra a sessão. O estado `uncertain` permanece visível.

CREATE TABLE bot_hooks (
    tenant_id uuid NOT NULL,
    inbox_id uuid NOT NULL,
    token_hash bytea NOT NULL,
    token_prefix text NOT NULL,
    deadline_seconds integer NOT NULL,
    enabled boolean NOT NULL,
    switch_generation bigint NOT NULL,
    version bigint NOT NULL,
    simulation text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, inbox_id),
    FOREIGN KEY (tenant_id, inbox_id) REFERENCES inboxes (tenant_id, id),
    CONSTRAINT bot_hooks_hash_chk CHECK (octet_length(token_hash) = 32),
    CONSTRAINT bot_hooks_prefix_chk CHECK (char_length(token_prefix) BETWEEN 4 AND 12),
    CONSTRAINT bot_hooks_deadline_chk CHECK (deadline_seconds BETWEEN 30 AND 86400),
    CONSTRAINT bot_hooks_generation_chk CHECK (switch_generation >= 1 AND version >= 1),
    CONSTRAINT bot_hooks_simulation_chk CHECK (simulation = 'in-process')
);

CREATE TABLE bot_sessions (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    inbox_id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    granted_epoch bigint NOT NULL,
    deadline_at timestamptz NOT NULL,
    state text NOT NULL,
    receipt text,
    handoff_id uuid,
    sector_id uuid,
    reason text,
    note text,
    notice text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, inbox_id) REFERENCES bot_hooks (tenant_id, inbox_id),
    FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    FOREIGN KEY (tenant_id, sector_id) REFERENCES sectors (tenant_id, id),
    CONSTRAINT bot_sessions_epoch_chk CHECK (granted_epoch >= 1),
    CONSTRAINT bot_sessions_state_chk CHECK (
        state IN (
            'pending', 'received', 'handed_off', 'uncertain',
            'expired', 'unavailable', 'switched_off'
        )
    ),
    CONSTRAINT bot_sessions_receipt_chk CHECK (receipt IS NULL OR receipt = 'received'),
    CONSTRAINT bot_sessions_reason_chk CHECK (
        reason IS NULL OR (char_length(reason) BETWEEN 1 AND 500 AND reason = btrim(reason))
    ),
    CONSTRAINT bot_sessions_note_chk CHECK (note IS NULL OR char_length(note) <= 500),
    CONSTRAINT bot_sessions_notice_chk CHECK (char_length(notice) <= 500),
    CONSTRAINT bot_sessions_handoff_chk CHECK (
        (state = 'handed_off' AND handoff_id IS NOT NULL AND reason IS NOT NULL)
        OR state <> 'handed_off'
    )
);

CREATE UNIQUE INDEX bot_sessions_epoch_key
    ON bot_sessions (tenant_id, conversation_id, granted_epoch);

CREATE UNIQUE INDEX bot_sessions_one_open
    ON bot_sessions (tenant_id, conversation_id)
    WHERE state IN ('pending', 'received');

CREATE INDEX bot_sessions_due
    ON bot_sessions (tenant_id, deadline_at, conversation_id)
    WHERE state IN ('pending', 'received');

CREATE INDEX bot_sessions_notices
    ON bot_sessions (tenant_id, inbox_id, updated_at DESC)
    WHERE notice <> '';

CREATE TABLE bot_hook_commands (
    tenant_id uuid NOT NULL,
    scope_id uuid NOT NULL,
    operation text NOT NULL,
    request_id uuid NOT NULL,
    request_hash bytea NOT NULL,
    response jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, scope_id, operation, request_id),
    FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT bot_hook_commands_operation_chk CHECK (
        operation IN ('install', 'rotate', 'switch', 'reply', 'handoff')
    ),
    CONSTRAINT bot_hook_commands_hash_chk CHECK (octet_length(request_hash) = 32),
    CONSTRAINT bot_hook_commands_response_chk CHECK (jsonb_typeof(response) = 'object')
);

-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
  FOREACH tab IN ARRAY ARRAY['bot_hooks', 'bot_sessions', 'bot_hook_commands'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', tab);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', tab);
    EXECUTE format($p$CREATE POLICY tenant_isolation ON %I TO chatisp_app USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)$p$, tab);
    EXECUTE format('REVOKE ALL ON TABLE %I FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress', tab);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE ON %I TO chatisp_app', tab);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback descartaria o gancho de bot e as posses concedidas';
END $$;
-- +goose StatementEnd
