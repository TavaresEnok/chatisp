-- +goose Up
-- Adiamento e marcadores de inatividade. O adiamento não é estado da conversa
-- e não encerra. A vaga sai porque o responsável deixa de ocupar assignee_id;
-- a preferência de retorno fica nesta tabela. Enquanto o adiamento está ativo,
-- a conversa não abre intervalo de fila: o round-robin existente só vê quem tem
-- intervalo aberto, e este módulo não altera o pacote distribution.

CREATE TABLE conversation_snoozes (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    snoozed_until timestamptz NOT NULL,
    reason text NOT NULL,
    destination_membership_id uuid NOT NULL,
    destination_sector_id uuid NOT NULL,
    state text NOT NULL,
    resolution text,
    created_by_membership_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    resolved_at timestamptz,
    PRIMARY KEY (tenant_id, id),
    FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    FOREIGN KEY (tenant_id, destination_membership_id) REFERENCES memberships (tenant_id, id),
    FOREIGN KEY (tenant_id, destination_sector_id) REFERENCES sectors (tenant_id, id),
    FOREIGN KEY (tenant_id, created_by_membership_id) REFERENCES memberships (tenant_id, id),
    CONSTRAINT conversation_snoozes_reason_chk CHECK (
        char_length(reason) BETWEEN 1 AND 500
        AND reason = btrim(reason)
    ),
    CONSTRAINT conversation_snoozes_state_chk CHECK (
        state IN ('active', 'returned', 'queued', 'cleared_by_customer')
    ),
    CONSTRAINT conversation_snoozes_resolution_chk CHECK (
        resolution IS NULL
        OR resolution IN ('assigned', 'queued', 'already_assigned', 'closed')
    ),
    CONSTRAINT conversation_snoozes_resolved_chk CHECK (
        (state = 'active' AND resolved_at IS NULL AND resolution IS NULL)
        OR (state <> 'active' AND resolved_at IS NOT NULL AND resolution IS NOT NULL)
    )
);

CREATE UNIQUE INDEX conversation_snoozes_one_active
    ON conversation_snoozes (tenant_id, conversation_id)
    WHERE state = 'active';

CREATE INDEX conversation_snoozes_due
    ON conversation_snoozes (tenant_id, snoozed_until, conversation_id)
    WHERE state = 'active';

CREATE TABLE inactivity_watches (
    tenant_id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    anchor_reply_at timestamptz NOT NULL,
    assignee_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation >= 1),
    warning_due_at timestamptz NOT NULL,
    close_due_at timestamptz,
    warning_state text NOT NULL,
    warning_at timestamptz,
    closed_at timestamptz,
    PRIMARY KEY (tenant_id, conversation_id),
    FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    FOREIGN KEY (tenant_id, assignee_id) REFERENCES memberships (tenant_id, id),
    CONSTRAINT inactivity_watches_state_chk CHECK (
        warning_state IN ('waiting', 'requested', 'sent', 'uncertain', 'cancelled')
    ),
    CONSTRAINT inactivity_watches_warning_chk CHECK (
        (warning_state IN ('waiting', 'requested') AND warning_at IS NULL AND close_due_at IS NULL)
        OR (warning_state = 'sent' AND warning_at IS NOT NULL AND close_due_at IS NOT NULL)
        OR (warning_state = 'uncertain' AND warning_at IS NOT NULL AND close_due_at IS NULL)
        OR (warning_state = 'cancelled')
    )
);

CREATE INDEX inactivity_watches_close_due
    ON inactivity_watches (tenant_id, close_due_at, conversation_id)
    WHERE warning_state = 'sent' AND closed_at IS NULL;

-- Pedido durável do aviso público. A geração amarra o pedido à âncora.
-- pending não encerra. state=confirmed é a decisão que armou o prazo.
-- state=uncertain não arma, mesmo quando o recibo recebido foi confirmed.
-- receipt_outcome guarda o resultado recebido, distinto dessa decisão.
CREATE TABLE lifecycle_warning_requests (
    tenant_id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation >= 1),
    anchor_reply_at timestamptz NOT NULL,
    assignee_id uuid NOT NULL,
    body text NOT NULL,
    state text NOT NULL,
    receipt_outcome text,
    requested_at timestamptz NOT NULL,
    resolved_at timestamptz,
    PRIMARY KEY (tenant_id, conversation_id, generation),
    FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    FOREIGN KEY (tenant_id, assignee_id) REFERENCES memberships (tenant_id, id),
    CONSTRAINT lifecycle_warning_requests_body_chk CHECK (char_length(body) BETWEEN 1 AND 500),
    CONSTRAINT lifecycle_warning_requests_state_chk CHECK (
        state IN ('pending', 'confirmed', 'uncertain')
    ),
    CONSTRAINT lifecycle_warning_requests_receipt_chk CHECK (
        (state = 'pending' AND receipt_outcome IS NULL)
        OR (state <> 'pending' AND receipt_outcome IN ('confirmed', 'uncertain'))
    ),
    CONSTRAINT lifecycle_warning_requests_resolved_chk CHECK (
        (state = 'pending' AND resolved_at IS NULL)
        OR (state <> 'pending' AND resolved_at IS NOT NULL)
    )
);

CREATE TABLE lifecycle_inactivity_settings (
    tenant_id uuid PRIMARY KEY REFERENCES tenants (id),
    warning_after_seconds integer NOT NULL CHECK (warning_after_seconds BETWEEN 60 AND 604800),
    close_after_seconds integer NOT NULL CHECK (close_after_seconds BETWEEN 60 AND 604800),
    version bigint NOT NULL CHECK (version > 0)
);

CREATE TABLE lifecycle_commands (
    tenant_id uuid NOT NULL,
    membership_id uuid NOT NULL,
    operation text NOT NULL,
    request_id uuid NOT NULL,
    request_hash bytea NOT NULL,
    response jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, membership_id, operation, request_id),
    FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships (tenant_id, id),
    CONSTRAINT lifecycle_commands_operation_chk CHECK (
        operation IN ('snooze', 'inactivity_settings')
    )
);

ALTER TABLE conversation_events DROP CONSTRAINT conversation_events_kind_chk;
ALTER TABLE conversation_events ADD CONSTRAINT conversation_events_kind_chk CHECK (
    kind IN (
        'assumed', 'controller_changed', 'closed', 'reopened',
        'wait_started', 'wait_ended', 'customer_message', 'outbound_queued',
        'snoozed', 'snooze_cleared'
    )
    OR kind ~ '^(transferred_member|transferred_sector|unassigned|distributed|snooze_return):[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}:[0-9a-f-]{36}$'
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION assignment_track_queue() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE queued boolean; previous_sector uuid; stamp timestamptz;
BEGIN
    queued := NEW.inbox_id IS NOT NULL
        AND NEW.sector_id IS NOT NULL
        AND NEW.assignee_id IS NULL
        AND NEW.status IN ('open', 'waiting_customer')
        AND NOT EXISTS (
            SELECT 1 FROM public.conversation_snoozes AS snooze
            WHERE snooze.tenant_id = NEW.tenant_id
              AND snooze.conversation_id = NEW.id
              AND snooze.state = 'active'
        );
    IF TG_OP = 'UPDATE' THEN
        previous_sector := OLD.sector_id;
    END IF;
    stamp := COALESCE(NEW.updated_at, now());
    IF TG_OP = 'INSERT' THEN
        stamp := COALESCE(NEW.created_at, stamp);
    END IF;
    IF NOT queued OR (TG_OP = 'UPDATE' AND previous_sector IS DISTINCT FROM NEW.sector_id) THEN
        UPDATE public.assignment_queue_intervals
           SET ended_at = GREATEST(started_at, stamp)
         WHERE tenant_id = NEW.tenant_id
           AND conversation_id = NEW.id
           AND ended_at IS NULL;
    END IF;
    IF queued THEN
        INSERT INTO public.assignment_queue_intervals (tenant_id, id, conversation_id, sector_id, started_at)
        VALUES (NEW.tenant_id, gen_random_uuid(), NEW.id, NEW.sector_id, stamp)
        ON CONFLICT (tenant_id, conversation_id) WHERE ended_at IS NULL DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
  FOREACH tab IN ARRAY ARRAY[
    'conversation_snoozes', 'inactivity_watches',
    'lifecycle_warning_requests',
    'lifecycle_inactivity_settings', 'lifecycle_commands'
  ] LOOP
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
    RAISE EXCEPTION 'rollback descartaria adiamentos e marcadores de inatividade';
END $$;
-- +goose StatementEnd
