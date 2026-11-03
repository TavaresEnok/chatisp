-- +goose Up
-- M5C triagem por caixa. O módulo grava menu, estado e histórico.
-- Não envia mensagem, não chama driver e não altera atendimento.

CREATE TABLE triage_menus (
    tenant_id uuid NOT NULL,
    inbox_id uuid NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    configured boolean NOT NULL,
    max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 5),
    options jsonb NOT NULL CHECK (jsonb_typeof(options) = 'array'),
    PRIMARY KEY (tenant_id, inbox_id),
    FOREIGN KEY (tenant_id, inbox_id) REFERENCES inboxes (tenant_id, id)
);

CREATE TABLE triage_states (
    tenant_id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    inbox_id uuid NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    welcome_sent boolean NOT NULL DEFAULT false,
    resolved boolean NOT NULL DEFAULT false,
    phase text NOT NULL DEFAULT '' CHECK (phase IN ('', 'menu', 'routed', 'exhausted', 'outside_hours', 'on_call')),
    menu_version bigint NOT NULL DEFAULT 0 CHECK (menu_version >= 0),
    cycle_number integer NOT NULL DEFAULT 0 CHECK (cycle_number >= 0),
    controller_epoch bigint NOT NULL DEFAULT 0 CHECK (controller_epoch >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, conversation_id),
    FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    FOREIGN KEY (tenant_id, inbox_id) REFERENCES inboxes (tenant_id, id)
);

CREATE TABLE triage_commands (
    tenant_id uuid NOT NULL,
    membership_id uuid NOT NULL,
    request_id uuid NOT NULL,
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'object'),
    PRIMARY KEY (tenant_id, membership_id, request_id),
    FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships (tenant_id, id)
);

CREATE TABLE triage_decisions (
    tenant_id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    input_id uuid NOT NULL,
    outcome jsonb NOT NULL CHECK (jsonb_typeof(outcome) = 'object'),
    PRIMARY KEY (tenant_id, conversation_id, input_id),
    FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id)
);

CREATE TABLE triage_events (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    input_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('menu', 'routed', 'exhausted', 'outside_hours', 'on_call')),
    detail jsonb NOT NULL CHECK (jsonb_typeof(detail) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id)
);

CREATE INDEX triage_events_conversation_idx ON triage_events (tenant_id, conversation_id, created_at);

-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
    FOREACH tab IN ARRAY ARRAY['triage_menus', 'triage_states', 'triage_commands', 'triage_decisions', 'triage_events'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', tab);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', tab);
        EXECUTE format('CREATE POLICY tenant_isolation ON %I TO chatisp_app USING (tenant_id = NULLIF(current_setting(''app.tenant_id'', true), '''')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting(''app.tenant_id'', true), '''')::uuid)', tab);
    END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE ON triage_menus, triage_states TO chatisp_app;
GRANT SELECT, INSERT ON triage_commands, triage_decisions, triage_events TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback descartaria menu, estado e histórico de triagem';
END $$;
-- +goose StatementEnd
