-- +goose Up
-- M2 attendance storage. The placeholder conversations row (id, tenant_id,
-- summary, created_at) stays valid: new columns have defaults or are nullable,
-- and an insert of only those three columns still succeeds.
--
-- Lock order for every transaction that takes more than one lock. Later locks
-- are never acquired before earlier ones. The cursor is always last so a
-- snapshot can hold it and see a committed frontier. No statement here opens
-- a network connection.
--   1. tenant_job_slots (tenant_id, queue_name)
--   2. inbox_send_leases (tenant_id, inbox_id)
--   3. protocol_counters (tenant_id, period)
--   4. conversations (tenant_id, id)
--   5. idempotency_records (tenant_id, principal_id, operation, client_request_id)
--   6. messages (tenant_id, id)
--   7. outbound_attempts (tenant_id, id)
--   8. realtime_tenant_cursors (tenant_id)

ALTER TABLE conversations
    ADD COLUMN inbox_id uuid,
    ADD COLUMN contact_id uuid,
    ADD COLUMN contact_identity_id uuid,
    ADD COLUMN status text NOT NULL DEFAULT 'open',
    ADD COLUMN controller text NOT NULL DEFAULT 'menu',
    ADD COLUMN controller_epoch bigint NOT NULL DEFAULT 1,
    ADD COLUMN version bigint NOT NULL DEFAULT 1,
    ADD COLUMN assignee_id uuid,
    ADD COLUMN protocol text,
    ADD COLUMN protocol_period text,
    ADD COLUMN protocol_communicated boolean NOT NULL DEFAULT false,
    ADD COLUMN external_protocol text,
    ADD COLUMN last_customer_message_at timestamptz,
    ADD COLUMN last_eligible_human_reply_at timestamptz,
    ADD COLUMN waiting_since timestamptz,
    ADD COLUMN cycle_number integer NOT NULL DEFAULT 0,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE conversations
    ADD CONSTRAINT conversations_inbox_fkey
        FOREIGN KEY (tenant_id, inbox_id) REFERENCES inboxes (tenant_id, id),
    ADD CONSTRAINT conversations_contact_fkey
        FOREIGN KEY (tenant_id, contact_id) REFERENCES contacts (tenant_id, id),
    ADD CONSTRAINT conversations_identity_fkey
        FOREIGN KEY (tenant_id, contact_identity_id) REFERENCES contact_channel_identities (tenant_id, id),
    ADD CONSTRAINT conversations_assignee_fkey
        FOREIGN KEY (tenant_id, assignee_id) REFERENCES memberships (tenant_id, id),
    ADD CONSTRAINT conversations_status_chk CHECK (status IN ('triage', 'open', 'waiting_customer', 'closed')),
    ADD CONSTRAINT conversations_controller_chk CHECK (
        controller IN ('menu', 'bot', 'human')
        AND (
            (controller = 'human' AND assignee_id IS NOT NULL)
            OR (controller <> 'human' AND assignee_id IS NULL)
        )
    ),
    ADD CONSTRAINT conversations_epoch_chk CHECK (controller_epoch >= 1),
    ADD CONSTRAINT conversations_version_chk CHECK (version >= 1),
    ADD CONSTRAINT conversations_cycle_chk CHECK (cycle_number >= 0),
    ADD CONSTRAINT conversations_shape_chk CHECK (
        (
            inbox_id IS NULL
            AND contact_id IS NULL
            AND contact_identity_id IS NULL
            AND protocol IS NULL
            AND cycle_number = 0
        )
        OR (
            inbox_id IS NOT NULL
            AND contact_id IS NOT NULL
            AND contact_identity_id IS NOT NULL
            AND protocol IS NOT NULL
            AND protocol_period IS NOT NULL
            AND cycle_number >= 1
        )
    ),
    ADD CONSTRAINT conversations_protocol_len CHECK (
        protocol IS NULL OR (char_length(protocol) BETWEEN 1 AND 64 AND protocol = btrim(protocol))
    ),
    ADD CONSTRAINT conversations_period_chk CHECK (
        protocol_period IS NULL OR protocol_period ~ '^[0-9]{6}$'
    ),
    ADD CONSTRAINT conversations_external_protocol_len CHECK (
        external_protocol IS NULL OR (
            char_length(external_protocol) BETWEEN 1 AND 128
            AND external_protocol = btrim(external_protocol)
        )
    );

-- One open attendance per tenant, inbox and external identity.
-- Placeholder rows have a null identity and do not compete.
CREATE UNIQUE INDEX conversations_one_active_identity
    ON conversations (tenant_id, inbox_id, contact_identity_id)
    WHERE status <> 'closed' AND contact_identity_id IS NOT NULL;

CREATE UNIQUE INDEX conversations_protocol_key
    ON conversations (tenant_id, protocol)
    WHERE protocol IS NOT NULL;

CREATE INDEX conversations_inbox_status_idx
    ON conversations (tenant_id, inbox_id, status, created_at)
    WHERE inbox_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION conversations_guard_protocol()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF OLD.protocol IS NOT NULL AND NEW.protocol IS DISTINCT FROM OLD.protocol THEN
        RAISE EXCEPTION 'protocolo imutável'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.protocol_period IS NOT NULL AND NEW.protocol_period IS DISTINCT FROM OLD.protocol_period THEN
        RAISE EXCEPTION 'protocolo imutável'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER conversations_guard_protocol
    BEFORE UPDATE OF protocol, protocol_period ON conversations
    FOR EACH ROW
    EXECUTE FUNCTION conversations_guard_protocol();

REVOKE ALL ON FUNCTION conversations_guard_protocol() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION conversations_guard_protocol() TO chatisp_app;

-- The contact on the conversation is the contact of the identity, and that
-- identity uses the same connector as the inbox. A placeholder has neither.
-- +goose StatementBegin
CREATE FUNCTION conversations_guard_binding()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    v_contact uuid;
    v_identity_connector uuid;
    v_inbox_connector uuid;
BEGIN
    IF NEW.inbox_id IS NULL AND NEW.contact_id IS NULL AND NEW.contact_identity_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF NEW.inbox_id IS NULL OR NEW.contact_id IS NULL OR NEW.contact_identity_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT identity.contact_id, identity.connector_id
      INTO v_contact, v_identity_connector
      FROM public.contact_channel_identities AS identity
     WHERE identity.tenant_id = NEW.tenant_id
       AND identity.id = NEW.contact_identity_id;
    IF NOT FOUND OR v_contact IS DISTINCT FROM NEW.contact_id THEN
        RAISE EXCEPTION 'amarração incompatível'
            USING ERRCODE = '23514';
    END IF;
    SELECT inbox.connector_id
      INTO v_inbox_connector
      FROM public.inboxes AS inbox
     WHERE inbox.tenant_id = NEW.tenant_id
       AND inbox.id = NEW.inbox_id;
    IF NOT FOUND OR v_inbox_connector IS DISTINCT FROM v_identity_connector THEN
        RAISE EXCEPTION 'amarração incompatível'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER conversations_guard_binding
    BEFORE INSERT OR UPDATE ON conversations
    FOR EACH ROW
    EXECUTE FUNCTION conversations_guard_binding();

REVOKE ALL ON FUNCTION conversations_guard_binding() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION conversations_guard_binding() TO chatisp_app;

GRANT UPDATE ON conversations TO chatisp_app;

CREATE TABLE protocol_counters (
    tenant_id uuid NOT NULL,
    period text NOT NULL,
    last_value bigint NOT NULL,
    PRIMARY KEY (tenant_id, period),
    CONSTRAINT protocol_counters_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT protocol_counters_period_chk CHECK (period ~ '^[0-9]{6}$'),
    CONSTRAINT protocol_counters_value_chk CHECK (last_value >= 1)
);

CREATE TABLE conversation_cycles (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    cycle_number integer NOT NULL,
    opened_at timestamptz NOT NULL,
    closed_at timestamptz,
    close_reason text,
    close_result text,
    close_summary text,
    close_author_id uuid,
    close_membership_id uuid,
    close_author_kind text,
    external_reference text,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT conversation_cycles_conversation_key UNIQUE (tenant_id, conversation_id, cycle_number),
    CONSTRAINT conversation_cycles_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT conversation_cycles_conversation_fkey
        FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    CONSTRAINT conversation_cycles_membership_fkey
        FOREIGN KEY (tenant_id, close_membership_id) REFERENCES memberships (tenant_id, id),
    CONSTRAINT conversation_cycles_number_chk CHECK (cycle_number >= 1),
    CONSTRAINT conversation_cycles_closure_chk CHECK (
        (
            closed_at IS NULL
            AND close_reason IS NULL
            AND close_result IS NULL
            AND close_summary IS NULL
            AND close_author_id IS NULL
            AND close_membership_id IS NULL
            AND close_author_kind IS NULL
        )
        OR (
            closed_at IS NOT NULL
            AND char_length(btrim(close_reason)) BETWEEN 1 AND 500
            AND close_result IN ('resolved', 'no_response', 'referred_external', 'spam')
            AND char_length(btrim(close_summary)) BETWEEN 1 AND 4000
            AND close_author_id IS NOT NULL
            AND close_membership_id IS NOT NULL
            AND close_author_kind IN ('human', 'bot')
        )
    ),
    CONSTRAINT conversation_cycles_external_len CHECK (
        external_reference IS NULL OR char_length(external_reference) <= 128
    )
);

CREATE INDEX conversation_cycles_conversation_idx
    ON conversation_cycles (tenant_id, conversation_id, cycle_number);

-- The closing membership and the stored author must be the same person.
-- The composite membership key already blocks another tenant's membership.
-- +goose StatementBegin
CREATE FUNCTION conversation_cycles_guard_author()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    v_user uuid;
BEGIN
    IF NEW.close_membership_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT m.user_id
      INTO v_user
      FROM public.memberships AS m
     WHERE m.tenant_id = NEW.tenant_id
       AND m.id = NEW.close_membership_id;
    IF v_user IS DISTINCT FROM NEW.close_author_id THEN
        RAISE EXCEPTION 'autoria não pertence ao atendimento'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER conversation_cycles_guard_author
    BEFORE INSERT OR UPDATE ON conversation_cycles
    FOR EACH ROW
    EXECUTE FUNCTION conversation_cycles_guard_author();

REVOKE ALL ON FUNCTION conversation_cycles_guard_author() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION conversation_cycles_guard_author() TO chatisp_app;

CREATE TABLE conversation_events (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    cycle_number integer NOT NULL,
    kind text NOT NULL,
    actor_id uuid,
    at timestamptz NOT NULL,
    version bigint NOT NULL,
    controller_epoch bigint NOT NULL,
    controller text NOT NULL,
    assignee_id uuid,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT conversation_events_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT conversation_events_conversation_fkey
        FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    CONSTRAINT conversation_events_kind_chk CHECK (
        kind IN (
            'assumed', 'controller_changed', 'closed', 'reopened',
            'wait_started', 'wait_ended', 'customer_message', 'outbound_queued'
        )
    ),
    CONSTRAINT conversation_events_controller_chk CHECK (controller IN ('menu', 'bot', 'human')),
    CONSTRAINT conversation_events_version_chk CHECK (version >= 1 AND controller_epoch >= 1 AND cycle_number >= 1)
);

CREATE INDEX conversation_events_conversation_idx
    ON conversation_events (tenant_id, conversation_id, at, id);

CREATE TABLE idempotency_records (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    principal_id uuid NOT NULL,
    operation text NOT NULL,
    client_request_id text NOT NULL,
    request_hash bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT idempotency_records_key UNIQUE (tenant_id, principal_id, operation, client_request_id),
    CONSTRAINT idempotency_records_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT idempotency_records_principal_fkey
        FOREIGN KEY (tenant_id, principal_id) REFERENCES memberships (tenant_id, id),
    CONSTRAINT idempotency_records_operation_chk CHECK (
        char_length(operation) BETWEEN 1 AND 64
        AND operation = btrim(operation)
    ),
    CONSTRAINT idempotency_records_request_chk CHECK (
        char_length(client_request_id) BETWEEN 1 AND 200
        AND client_request_id = btrim(client_request_id)
    ),
    CONSTRAINT idempotency_records_hash_chk CHECK (octet_length(request_hash) = 32)
);

CREATE TABLE messages (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    connector_id uuid NOT NULL,
    idempotency_id uuid,
    external_message_id text,
    direction text NOT NULL,
    origin text NOT NULL,
    visibility text NOT NULL,
    body text NOT NULL DEFAULT '',
    local_state text,
    controller text,
    controller_epoch bigint,
    client_request_id text,
    author_membership_id uuid,
    trusted_at timestamptz,
    ingested_at timestamptz NOT NULL DEFAULT now(),
    historical boolean NOT NULL DEFAULT false,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT messages_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT messages_conversation_fkey
        FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    CONSTRAINT messages_connector_fkey
        FOREIGN KEY (tenant_id, connector_id) REFERENCES channel_connectors (tenant_id, id),
    CONSTRAINT messages_idempotency_fkey
        FOREIGN KEY (tenant_id, idempotency_id) REFERENCES idempotency_records (tenant_id, id),
    CONSTRAINT messages_author_fkey
        FOREIGN KEY (tenant_id, author_membership_id) REFERENCES memberships (tenant_id, id),
    CONSTRAINT messages_direction_chk CHECK (direction IN ('inbound', 'outbound')),
    CONSTRAINT messages_origin_chk CHECK (origin IN ('customer', 'human', 'bot', 'menu', 'note', 'technical')),
    CONSTRAINT messages_visibility_chk CHECK (visibility IN ('public', 'internal')),
    CONSTRAINT messages_state_chk CHECK (
        local_state IS NULL OR local_state IN (
            'received', 'queued', 'sending', 'accepted', 'failed', 'retrying', 'uncertain', 'cancelled'
        )
    ),
    CONSTRAINT messages_body_len CHECK (char_length(body) <= 16000),
    CONSTRAINT messages_external_len CHECK (
        external_message_id IS NULL OR (
            char_length(external_message_id) BETWEEN 1 AND 256
            AND external_message_id = btrim(external_message_id)
        )
    )
);

CREATE UNIQUE INDEX messages_external_key
    ON messages (tenant_id, connector_id, external_message_id)
    WHERE external_message_id IS NOT NULL;

CREATE UNIQUE INDEX messages_idempotency_key
    ON messages (tenant_id, idempotency_id)
    WHERE idempotency_id IS NOT NULL;

CREATE INDEX messages_timeline_idx
    ON messages (tenant_id, conversation_id, ingested_at, id);

-- A visible conversation must be sending through its inbox connector.
-- A missing conversation is left to the composite foreign key (23503).
-- +goose StatementBegin
CREATE FUNCTION messages_guard_binding()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    v_inbox uuid;
    v_connector uuid;
BEGIN
    SELECT conversation.inbox_id, inbox.connector_id
      INTO v_inbox, v_connector
      FROM public.conversations AS conversation
      LEFT JOIN public.inboxes AS inbox
        ON inbox.tenant_id = conversation.tenant_id
       AND inbox.id = conversation.inbox_id
     WHERE conversation.tenant_id = NEW.tenant_id
       AND conversation.id = NEW.conversation_id;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;
    IF v_inbox IS NULL OR v_connector IS DISTINCT FROM NEW.connector_id THEN
        RAISE EXCEPTION 'conector não pertence à caixa'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER messages_guard_binding
    BEFORE INSERT OR UPDATE OF conversation_id, connector_id ON messages
    FOR EACH ROW
    EXECUTE FUNCTION messages_guard_binding();

REVOKE ALL ON FUNCTION messages_guard_binding() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION messages_guard_binding() TO chatisp_app;

-- Status is its own entity. Dedup key is documented in docs/contracts/m2-storage.md.
-- A status may arrive before the message; message_id stays null until linked.
CREATE TABLE message_status_events (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    connector_id uuid NOT NULL,
    message_id uuid,
    external_message_id text,
    dedup_key text NOT NULL,
    kind text NOT NULL,
    trusted_at timestamptz,
    ingested_at timestamptz NOT NULL DEFAULT now(),
    billing_category text,
    billable text NOT NULL DEFAULT 'unknown',
    pending boolean NOT NULL DEFAULT false,
    detail text NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT message_status_events_dedup_key UNIQUE (tenant_id, connector_id, dedup_key),
    CONSTRAINT message_status_events_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT message_status_events_connector_fkey
        FOREIGN KEY (tenant_id, connector_id) REFERENCES channel_connectors (tenant_id, id),
    CONSTRAINT message_status_events_message_fkey
        FOREIGN KEY (tenant_id, message_id) REFERENCES messages (tenant_id, id),
    CONSTRAINT message_status_events_kind_chk CHECK (kind IN ('accepted', 'delivered', 'read', 'failed', 'unknown')),
    CONSTRAINT message_status_events_billable_chk CHECK (billable IN ('unknown', 'true', 'false')),
    CONSTRAINT message_status_events_dedup_len CHECK (char_length(dedup_key) BETWEEN 1 AND 400),
    CONSTRAINT message_status_events_detail_len CHECK (char_length(detail) <= 500),
    CONSTRAINT message_status_events_pending_chk CHECK (
        (pending = true AND message_id IS NULL) OR pending = false
    )
);

CREATE INDEX message_status_events_message_idx
    ON message_status_events (tenant_id, message_id, ingested_at)
    WHERE message_id IS NOT NULL;

-- A status that names a message must use that message's connector.
-- +goose StatementBegin
CREATE FUNCTION message_status_guard_binding()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    v_connector uuid;
BEGIN
    IF NEW.message_id IS NULL THEN
        RETURN NEW;
    END IF;
    SELECT message.connector_id
      INTO v_connector
      FROM public.messages AS message
     WHERE message.tenant_id = NEW.tenant_id
       AND message.id = NEW.message_id;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;
    IF v_connector IS DISTINCT FROM NEW.connector_id THEN
        RAISE EXCEPTION 'conector não pertence à caixa'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER message_status_guard_binding
    BEFORE INSERT OR UPDATE OF message_id, connector_id ON message_status_events
    FOR EACH ROW
    EXECUTE FUNCTION message_status_guard_binding();

REVOKE ALL ON FUNCTION message_status_guard_binding() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION message_status_guard_binding() TO chatisp_app;

CREATE TABLE outbound_attempts (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    message_id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    idempotency_id uuid,
    attempt_no integer NOT NULL,
    state text NOT NULL,
    controller text NOT NULL,
    controller_epoch bigint NOT NULL,
    origin text NOT NULL,
    lease_version bigint,
    dispatched_at timestamptz,
    driver_ref text,
    error_code text,
    error_message text,
    previous_attempt_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT outbound_attempts_message_attempt_key UNIQUE (tenant_id, message_id, attempt_no),
    CONSTRAINT outbound_attempts_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT outbound_attempts_message_fkey
        FOREIGN KEY (tenant_id, message_id) REFERENCES messages (tenant_id, id),
    CONSTRAINT outbound_attempts_conversation_fkey
        FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations (tenant_id, id),
    CONSTRAINT outbound_attempts_idempotency_fkey
        FOREIGN KEY (tenant_id, idempotency_id) REFERENCES idempotency_records (tenant_id, id),
    CONSTRAINT outbound_attempts_previous_fkey
        FOREIGN KEY (tenant_id, previous_attempt_id) REFERENCES outbound_attempts (tenant_id, id),
    CONSTRAINT outbound_attempts_state_chk CHECK (
        state IN ('queued', 'sending', 'accepted', 'failed', 'retrying', 'uncertain', 'cancelled')
    ),
    CONSTRAINT outbound_attempts_controller_chk CHECK (controller IN ('menu', 'bot', 'human')),
    CONSTRAINT outbound_attempts_origin_chk CHECK (origin IN ('human', 'bot', 'menu', 'note', 'technical')),
    CONSTRAINT outbound_attempts_no_chk CHECK (attempt_no >= 1),
    CONSTRAINT outbound_attempts_epoch_chk CHECK (controller_epoch >= 1),
    CONSTRAINT outbound_attempts_error_len CHECK (
        (error_message IS NULL OR char_length(error_message) <= 500)
        AND (error_code IS NULL OR char_length(error_code) <= 80)
        AND (driver_ref IS NULL OR char_length(driver_ref) <= 256)
    )
);

CREATE UNIQUE INDEX outbound_attempts_idempotency_key
    ON outbound_attempts (tenant_id, idempotency_id)
    WHERE idempotency_id IS NOT NULL;

CREATE INDEX outbound_attempts_state_idx
    ON outbound_attempts (tenant_id, state, created_at);

-- Uncertain is not an automatic resend. A later receipt may still accept or fail it.
-- A new human send is a new row.
-- +goose StatementBegin
CREATE FUNCTION outbound_attempts_guard_state()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF OLD.state = NEW.state THEN
        RETURN NEW;
    END IF;
    IF OLD.state = 'queued' AND NEW.state NOT IN ('sending', 'cancelled', 'failed') THEN
        RAISE EXCEPTION 'transição de envio inválida'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.state = 'sending' AND NEW.state NOT IN ('accepted', 'failed', 'retrying', 'uncertain') THEN
        RAISE EXCEPTION 'transição de envio inválida'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.state = 'retrying' AND NEW.state NOT IN ('sending', 'failed', 'uncertain') THEN
        RAISE EXCEPTION 'transição de envio inválida'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.state = 'uncertain' AND NEW.state NOT IN ('accepted', 'failed') THEN
        RAISE EXCEPTION 'envio incerto não se repete'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.state IN ('accepted', 'failed', 'cancelled') THEN
        RAISE EXCEPTION 'tentativa de envio encerrada'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER outbound_attempts_guard_state
    BEFORE UPDATE OF state ON outbound_attempts
    FOR EACH ROW
    EXECUTE FUNCTION outbound_attempts_guard_state();

REVOKE ALL ON FUNCTION outbound_attempts_guard_state() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION outbound_attempts_guard_state() TO chatisp_app;

CREATE TABLE inbox_send_leases (
    tenant_id uuid NOT NULL,
    inbox_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    lease_version bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, inbox_id),
    CONSTRAINT inbox_send_leases_inbox_fkey
        FOREIGN KEY (tenant_id, inbox_id) REFERENCES inboxes (tenant_id, id),
    CONSTRAINT inbox_send_leases_version_chk CHECK (lease_version >= 1)
);

CREATE TABLE send_reservations (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    inbox_id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    owner_id uuid NOT NULL,
    lease_version bigint NOT NULL,
    expires_at timestamptz NOT NULL,
    state text NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT send_reservations_attempt_key UNIQUE (tenant_id, attempt_id),
    CONSTRAINT send_reservations_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT send_reservations_inbox_fkey
        FOREIGN KEY (tenant_id, inbox_id) REFERENCES inboxes (tenant_id, id),
    CONSTRAINT send_reservations_attempt_fkey
        FOREIGN KEY (tenant_id, attempt_id) REFERENCES outbound_attempts (tenant_id, id),
    CONSTRAINT send_reservations_state_chk CHECK (state IN ('held', 'consumed', 'released')),
    CONSTRAINT send_reservations_version_chk CHECK (lease_version >= 1)
);

CREATE TABLE tenant_job_slots (
    tenant_id uuid NOT NULL,
    queue_name text NOT NULL,
    max_slots integer NOT NULL,
    PRIMARY KEY (tenant_id, queue_name),
    CONSTRAINT tenant_job_slots_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT tenant_job_slots_queue_chk CHECK (queue_name IN ('ingress', 'inbound', 'outbound')),
    CONSTRAINT tenant_job_slots_max_chk CHECK (max_slots BETWEEN 1 AND 64)
);

CREATE TABLE tenant_job_reservations (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    queue_name text NOT NULL,
    owner_id uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT tenant_job_reservations_slot_fkey
        FOREIGN KEY (tenant_id, queue_name) REFERENCES tenant_job_slots (tenant_id, queue_name),
    CONSTRAINT tenant_job_reservations_queue_chk CHECK (queue_name IN ('ingress', 'inbound', 'outbound'))
);

CREATE INDEX tenant_job_reservations_live_idx
    ON tenant_job_reservations (tenant_id, queue_name, expires_at);

CREATE TABLE realtime_tenant_cursors (
    tenant_id uuid PRIMARY KEY,
    seq bigint NOT NULL DEFAULT 0,
    retain_after bigint NOT NULL DEFAULT 0,
    CONSTRAINT realtime_tenant_cursors_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT realtime_tenant_cursors_seq_chk CHECK (seq >= 0 AND retain_after >= 0 AND retain_after <= seq)
);

CREATE TABLE realtime_events (
    tenant_id uuid NOT NULL,
    seq bigint NOT NULL,
    id uuid NOT NULL,
    event_type text NOT NULL,
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    version bigint NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, seq),
    CONSTRAINT realtime_events_id_key UNIQUE (tenant_id, id),
    CONSTRAINT realtime_events_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT realtime_events_type_chk CHECK (
        char_length(event_type) BETWEEN 1 AND 80
        AND char_length(aggregate_type) BETWEEN 1 AND 80
    ),
    CONSTRAINT realtime_events_payload_obj CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT realtime_events_version_chk CHECK (version >= 1 AND seq >= 1)
);

-- Raw authenticated envelope. No tenant column: one body may name many tenants.
-- chatisp_app and chatisp_platform have no privilege on this table.
CREATE TABLE ingress_deliveries (
    id uuid PRIMARY KEY,
    source text NOT NULL,
    app_id text NOT NULL,
    body_sha256 bytea NOT NULL,
    body bytea NOT NULL,
    state text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ingress_deliveries_source_chk CHECK (source = 'meta_cloud'),
    CONSTRAINT ingress_deliveries_app_chk CHECK (
        char_length(app_id) BETWEEN 1 AND 64
        AND app_id = btrim(app_id)
    ),
    CONSTRAINT ingress_deliveries_hash_chk CHECK (octet_length(body_sha256) = 32),
    CONSTRAINT ingress_deliveries_body_chk CHECK (octet_length(body) BETWEEN 1 AND 1048576),
    CONSTRAINT ingress_deliveries_state_chk CHECK (state IN ('stored', 'routed', 'quarantined')),
    CONSTRAINT ingress_deliveries_replay_key UNIQUE (source, app_id, body_sha256)
);

CREATE TABLE ingress_quarantine (
    id uuid PRIMARY KEY,
    delivery_id uuid NOT NULL,
    reason text NOT NULL,
    remote_ref text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ingress_quarantine_delivery_fkey
        FOREIGN KEY (delivery_id) REFERENCES ingress_deliveries (id),
    CONSTRAINT ingress_quarantine_reason_chk CHECK (
        reason IN ('unknown_phone', 'incompatible_binding')
        AND char_length(remote_ref) <= 256
    )
);

CREATE TABLE meta_apps (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    app_id text NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT meta_apps_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT meta_apps_app_id_key UNIQUE (app_id),
    CONSTRAINT meta_apps_app_chk CHECK (
        char_length(app_id) BETWEEN 1 AND 64
        AND app_id = btrim(app_id)
    ),
    CONSTRAINT meta_apps_status_chk CHECK (status IN ('active', 'disabled'))
);

-- Ciphertext only. The master key stays outside the database.
CREATE TABLE meta_app_secrets (
    tenant_id uuid NOT NULL,
    meta_app_id uuid NOT NULL,
    key_version text NOT NULL,
    ciphertext bytea NOT NULL,
    aad text NOT NULL,
    PRIMARY KEY (tenant_id, meta_app_id),
    CONSTRAINT meta_app_secrets_app_fkey
        FOREIGN KEY (tenant_id, meta_app_id) REFERENCES meta_apps (tenant_id, id),
    CONSTRAINT meta_app_secrets_version_chk CHECK (char_length(key_version) BETWEEN 1 AND 64),
    CONSTRAINT meta_app_secrets_cipher_chk CHECK (octet_length(ciphertext) BETWEEN 1 AND 8192),
    CONSTRAINT meta_app_secrets_aad_chk CHECK (char_length(aad) BETWEEN 1 AND 500)
);

CREATE TABLE meta_wabas (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    meta_app_id uuid NOT NULL,
    waba_id text NOT NULL,
    status text NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT meta_wabas_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT meta_wabas_app_fkey
        FOREIGN KEY (tenant_id, meta_app_id) REFERENCES meta_apps (tenant_id, id),
    CONSTRAINT meta_wabas_remote_key UNIQUE (waba_id),
    CONSTRAINT meta_wabas_remote_chk CHECK (
        char_length(waba_id) BETWEEN 1 AND 64
        AND waba_id = btrim(waba_id)
    ),
    CONSTRAINT meta_wabas_status_chk CHECK (status IN ('active', 'disabled'))
);

CREATE TABLE meta_phone_numbers (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    meta_waba_id uuid NOT NULL,
    phone_number_id text NOT NULL,
    display_number text,
    status text NOT NULL,
    connector_id uuid NOT NULL,
    inbox_id uuid NOT NULL,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT meta_phone_numbers_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT meta_phone_numbers_waba_fkey
        FOREIGN KEY (tenant_id, meta_waba_id) REFERENCES meta_wabas (tenant_id, id),
    CONSTRAINT meta_phone_numbers_connector_fkey
        FOREIGN KEY (tenant_id, connector_id) REFERENCES channel_connectors (tenant_id, id),
    CONSTRAINT meta_phone_numbers_inbox_fkey
        FOREIGN KEY (tenant_id, inbox_id) REFERENCES inboxes (tenant_id, id),
    CONSTRAINT meta_phone_numbers_remote_key UNIQUE (phone_number_id),
    CONSTRAINT meta_phone_numbers_remote_chk CHECK (
        char_length(phone_number_id) BETWEEN 1 AND 64
        AND phone_number_id = btrim(phone_number_id)
    ),
    CONSTRAINT meta_phone_numbers_status_chk CHECK (status IN ('active', 'disabled'))
);

CREATE TABLE credential_secrets (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    entity text NOT NULL,
    entity_id uuid NOT NULL,
    field text NOT NULL,
    key_version text NOT NULL,
    ciphertext bytea NOT NULL,
    aad text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT credential_secrets_entity_key UNIQUE (tenant_id, entity, entity_id, field),
    CONSTRAINT credential_secrets_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT credential_secrets_entity_chk CHECK (entity IN ('channel_connector')),
    CONSTRAINT credential_secrets_field_chk CHECK (
        char_length(field) BETWEEN 1 AND 64
        AND field = btrim(field)
    ),
    CONSTRAINT credential_secrets_version_chk CHECK (char_length(key_version) BETWEEN 1 AND 64),
    CONSTRAINT credential_secrets_cipher_chk CHECK (octet_length(ciphertext) BETWEEN 1 AND 8192),
    CONSTRAINT credential_secrets_aad_chk CHECK (char_length(aad) BETWEEN 1 AND 500)
);

CREATE TABLE webhook_items (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    delivery_id uuid NOT NULL,
    inbox_id uuid NOT NULL,
    connector_id uuid NOT NULL,
    phone_number_id text NOT NULL,
    external_key text NOT NULL,
    kind text NOT NULL,
    state text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT webhook_items_delivery_key UNIQUE (delivery_id, external_key),
    CONSTRAINT webhook_items_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT webhook_items_delivery_fkey
        FOREIGN KEY (delivery_id) REFERENCES ingress_deliveries (id),
    CONSTRAINT webhook_items_inbox_fkey
        FOREIGN KEY (tenant_id, inbox_id) REFERENCES inboxes (tenant_id, id),
    CONSTRAINT webhook_items_connector_fkey
        FOREIGN KEY (tenant_id, connector_id) REFERENCES channel_connectors (tenant_id, id),
    CONSTRAINT webhook_items_kind_chk CHECK (kind IN ('message', 'status', 'other')),
    CONSTRAINT webhook_items_state_chk CHECK (state IN ('pending', 'processed', 'quarantined')),
    CONSTRAINT webhook_items_key_chk CHECK (
        char_length(external_key) BETWEEN 1 AND 256
        AND external_key = btrim(external_key)
    ),
    CONSTRAINT webhook_items_phone_chk CHECK (char_length(phone_number_id) BETWEEN 1 AND 64),
    CONSTRAINT webhook_items_payload_obj CHECK (jsonb_typeof(payload) = 'object')
);

CREATE INDEX webhook_items_inbox_idx ON webhook_items (tenant_id, inbox_id, created_at);

-- (tenant_id, id) already identifies an inbox. This key lets a row that
-- carries both inbox_id and connector_id reference that pair.
ALTER TABLE inboxes
    ADD CONSTRAINT inboxes_id_connector_key UNIQUE (tenant_id, id, connector_id);

ALTER TABLE meta_phone_numbers
    ADD CONSTRAINT meta_phone_numbers_inbox_connector_fkey
    FOREIGN KEY (tenant_id, inbox_id, connector_id)
    REFERENCES inboxes (tenant_id, id, connector_id);

ALTER TABLE webhook_items
    ADD CONSTRAINT webhook_items_inbox_connector_fkey
    FOREIGN KEY (tenant_id, inbox_id, connector_id)
    REFERENCES inboxes (tenant_id, id, connector_id);

-- Row security. Missing app.tenant_id matches nothing for the app role.
-- Ingress reads the routing registry and inserts segregated items. It does
-- not receive a policy on messages, conversations or credentials.
-- ingress_deliveries and ingress_quarantine stay global and grant-restricted.
-- They have no tenant_id. Enabling RLS with no policy would hide them from
-- the ingress role as well, so access is the GRANT list below.

ALTER TABLE protocol_counters ENABLE ROW LEVEL SECURITY;
ALTER TABLE protocol_counters FORCE ROW LEVEL SECURITY;
ALTER TABLE conversation_cycles ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversation_cycles FORCE ROW LEVEL SECURITY;
ALTER TABLE conversation_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversation_events FORCE ROW LEVEL SECURITY;
ALTER TABLE idempotency_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE idempotency_records FORCE ROW LEVEL SECURITY;
ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE messages FORCE ROW LEVEL SECURITY;
ALTER TABLE message_status_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE message_status_events FORCE ROW LEVEL SECURITY;
ALTER TABLE outbound_attempts ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbound_attempts FORCE ROW LEVEL SECURITY;
ALTER TABLE inbox_send_leases ENABLE ROW LEVEL SECURITY;
ALTER TABLE inbox_send_leases FORCE ROW LEVEL SECURITY;
ALTER TABLE send_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE send_reservations FORCE ROW LEVEL SECURITY;
ALTER TABLE tenant_job_slots ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_job_slots FORCE ROW LEVEL SECURITY;
ALTER TABLE tenant_job_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_job_reservations FORCE ROW LEVEL SECURITY;
ALTER TABLE realtime_tenant_cursors ENABLE ROW LEVEL SECURITY;
ALTER TABLE realtime_tenant_cursors FORCE ROW LEVEL SECURITY;
ALTER TABLE realtime_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE realtime_events FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_apps ENABLE ROW LEVEL SECURITY;
ALTER TABLE meta_apps FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE meta_app_secrets FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_wabas ENABLE ROW LEVEL SECURITY;
ALTER TABLE meta_wabas FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_phone_numbers ENABLE ROW LEVEL SECURITY;
ALTER TABLE meta_phone_numbers FORCE ROW LEVEL SECURITY;
ALTER TABLE credential_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE credential_secrets FORCE ROW LEVEL SECURITY;
ALTER TABLE webhook_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_items FORCE ROW LEVEL SECURITY;

CREATE POLICY protocol_counters_app_isolation ON protocol_counters
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY conversation_cycles_app_isolation ON conversation_cycles
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY conversation_events_app_isolation ON conversation_events
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY idempotency_records_app_isolation ON idempotency_records
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY messages_app_isolation ON messages
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY message_status_events_app_isolation ON message_status_events
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY outbound_attempts_app_isolation ON outbound_attempts
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY inbox_send_leases_app_isolation ON inbox_send_leases
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY send_reservations_app_isolation ON send_reservations
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_job_slots_app_isolation ON tenant_job_slots
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_job_reservations_app_isolation ON tenant_job_reservations
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY realtime_tenant_cursors_app_isolation ON realtime_tenant_cursors
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY realtime_events_app_isolation ON realtime_events
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY meta_apps_app_isolation ON meta_apps
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY meta_app_secrets_app_isolation ON meta_app_secrets
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY meta_wabas_app_isolation ON meta_wabas
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY meta_phone_numbers_app_isolation ON meta_phone_numbers
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY credential_secrets_app_isolation ON credential_secrets
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY webhook_items_app_isolation ON webhook_items
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY meta_apps_ingress_read ON meta_apps
    FOR SELECT
    TO chatisp_ingress
    USING (current_setting('app.access_scope', true) = 'ingress');

CREATE POLICY meta_app_secrets_ingress_read ON meta_app_secrets
    FOR SELECT
    TO chatisp_ingress
    USING (current_setting('app.access_scope', true) = 'ingress');

CREATE POLICY meta_wabas_ingress_read ON meta_wabas
    FOR SELECT
    TO chatisp_ingress
    USING (current_setting('app.access_scope', true) = 'ingress');

CREATE POLICY meta_phone_numbers_ingress_read ON meta_phone_numbers
    FOR SELECT
    TO chatisp_ingress
    USING (current_setting('app.access_scope', true) = 'ingress');

-- Ingress may see item keys to deduplicate a replay. The payload column
-- stays ungranted, so the raw message body is not readable here.
CREATE POLICY webhook_items_ingress_key ON webhook_items
    FOR SELECT
    TO chatisp_ingress
    USING (current_setting('app.access_scope', true) = 'ingress');

CREATE POLICY webhook_items_ingress_insert ON webhook_items
    FOR INSERT
    TO chatisp_ingress
    WITH CHECK (
        current_setting('app.access_scope', true) = 'ingress'
        AND EXISTS (
            SELECT 1
            FROM meta_phone_numbers AS phone
            WHERE phone.tenant_id = webhook_items.tenant_id
              AND phone.inbox_id = webhook_items.inbox_id
              AND phone.connector_id = webhook_items.connector_id
              AND phone.phone_number_id = webhook_items.phone_number_id
              AND phone.status = 'active'
        )
    );

REVOKE ALL ON TABLE protocol_counters FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE conversation_cycles FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE conversation_events FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE idempotency_records FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE messages FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE message_status_events FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE outbound_attempts FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE inbox_send_leases FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE send_reservations FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE tenant_job_slots FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE tenant_job_reservations FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE realtime_tenant_cursors FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE realtime_events FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE ingress_deliveries FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE ingress_quarantine FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE meta_apps FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE meta_app_secrets FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE meta_wabas FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE meta_phone_numbers FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE credential_secrets FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE webhook_items FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;

GRANT SELECT, INSERT, UPDATE ON protocol_counters TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON conversation_cycles TO chatisp_app;
GRANT SELECT, INSERT ON conversation_events TO chatisp_app;
GRANT SELECT, INSERT ON idempotency_records TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON messages TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON message_status_events TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON outbound_attempts TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON inbox_send_leases TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON send_reservations TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON tenant_job_slots TO chatisp_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_job_reservations TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON realtime_tenant_cursors TO chatisp_app;
GRANT SELECT, INSERT, DELETE ON realtime_events TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON meta_apps TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON meta_app_secrets TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON meta_wabas TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON meta_phone_numbers TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON credential_secrets TO chatisp_app;
GRANT SELECT, UPDATE ON webhook_items TO chatisp_app;

GRANT SELECT, INSERT, UPDATE ON ingress_deliveries TO chatisp_ingress;
GRANT SELECT, INSERT ON ingress_quarantine TO chatisp_ingress;
GRANT SELECT ON meta_apps TO chatisp_ingress;
GRANT SELECT ON meta_app_secrets TO chatisp_ingress;
GRANT SELECT ON meta_wabas TO chatisp_ingress;
GRANT SELECT ON meta_phone_numbers TO chatisp_ingress;
GRANT SELECT (id, tenant_id, delivery_id, external_key) ON webhook_items TO chatisp_ingress;
GRANT INSERT ON webhook_items TO chatisp_ingress;

-- +goose Down
DROP TABLE IF EXISTS webhook_items;
DROP TABLE IF EXISTS credential_secrets;
DROP TABLE IF EXISTS meta_phone_numbers;
DROP TABLE IF EXISTS meta_wabas;
DROP TABLE IF EXISTS meta_app_secrets;
DROP TABLE IF EXISTS meta_apps;
DROP TABLE IF EXISTS ingress_quarantine;
DROP TABLE IF EXISTS ingress_deliveries;
DROP TABLE IF EXISTS realtime_events;
DROP TABLE IF EXISTS realtime_tenant_cursors;
DROP TABLE IF EXISTS tenant_job_reservations;
DROP TABLE IF EXISTS tenant_job_slots;
DROP TABLE IF EXISTS send_reservations;
DROP TABLE IF EXISTS inbox_send_leases;
DROP TRIGGER IF EXISTS outbound_attempts_guard_state ON outbound_attempts;
DROP FUNCTION IF EXISTS outbound_attempts_guard_state();
DROP TABLE IF EXISTS outbound_attempts;
DROP TRIGGER IF EXISTS message_status_guard_binding ON message_status_events;
DROP FUNCTION IF EXISTS message_status_guard_binding();
DROP TABLE IF EXISTS message_status_events;
DROP TRIGGER IF EXISTS messages_guard_binding ON messages;
DROP FUNCTION IF EXISTS messages_guard_binding();
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS idempotency_records;
DROP TABLE IF EXISTS conversation_events;
DROP TRIGGER IF EXISTS conversation_cycles_guard_author ON conversation_cycles;
DROP FUNCTION IF EXISTS conversation_cycles_guard_author();
DROP TABLE IF EXISTS conversation_cycles;
DROP TABLE IF EXISTS protocol_counters;
REVOKE UPDATE ON conversations FROM chatisp_app;
DROP TRIGGER IF EXISTS conversations_guard_binding ON conversations;
DROP FUNCTION IF EXISTS conversations_guard_binding();
DROP TRIGGER IF EXISTS conversations_guard_protocol ON conversations;
DROP FUNCTION IF EXISTS conversations_guard_protocol();
ALTER TABLE inboxes DROP CONSTRAINT IF EXISTS inboxes_id_connector_key;
ALTER TABLE conversations
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS cycle_number,
    DROP COLUMN IF EXISTS waiting_since,
    DROP COLUMN IF EXISTS last_eligible_human_reply_at,
    DROP COLUMN IF EXISTS last_customer_message_at,
    DROP COLUMN IF EXISTS external_protocol,
    DROP COLUMN IF EXISTS protocol_communicated,
    DROP COLUMN IF EXISTS protocol_period,
    DROP COLUMN IF EXISTS protocol,
    DROP COLUMN IF EXISTS assignee_id,
    DROP COLUMN IF EXISTS version,
    DROP COLUMN IF EXISTS controller_epoch,
    DROP COLUMN IF EXISTS controller,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS contact_identity_id,
    DROP COLUMN IF EXISTS contact_id,
    DROP COLUMN IF EXISTS inbox_id;
