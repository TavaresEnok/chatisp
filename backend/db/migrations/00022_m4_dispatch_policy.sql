-- +goose Up
-- Message kind/context for freeform vs template. Existing rows stay plain text.
ALTER TABLE messages
    ADD COLUMN content_kind text NOT NULL DEFAULT 'text',
    ADD COLUMN content_context jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE messages
    ADD CONSTRAINT messages_content_kind_chk CHECK (content_kind IN ('text', 'template')),
    ADD CONSTRAINT messages_content_context_chk CHECK (jsonb_typeof(content_context) = 'object');

-- Durable policy photographs. Dispatch rows carry the lease that fenced them.
-- A later row for the same attempt is a new observation, not a reusable token.
CREATE TABLE dispatch_policy_decisions (
    tenant_id uuid NOT NULL,
    id uuid NOT NULL,
    attempt_id uuid NOT NULL,
    phase text NOT NULL,
    lease_version bigint,
    allowed boolean NOT NULL,
    reason text NOT NULL,
    driver text NOT NULL DEFAULT '',
    official_window boolean NOT NULL,
    evaluated_at timestamptz NOT NULL,
    window_ends_at timestamptz,
    mode text NOT NULL,
    kind text NOT NULL,
    purpose text NOT NULL,
    template_id text NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT dispatch_policy_decisions_attempt_fkey
        FOREIGN KEY (tenant_id, attempt_id) REFERENCES outbound_attempts (tenant_id, id),
    CONSTRAINT dispatch_policy_decisions_phase_chk CHECK (phase IN ('enqueue', 'dispatch')),
    CONSTRAINT dispatch_policy_decisions_reason_chk CHECK (char_length(reason) BETWEEN 1 AND 80),
    CONSTRAINT dispatch_policy_decisions_mode_chk CHECK (mode IN ('requested', 'proactive')),
    CONSTRAINT dispatch_policy_decisions_kind_chk CHECK (kind IN ('freeform', 'template')),
    CONSTRAINT dispatch_policy_decisions_purpose_chk CHECK (
        char_length(purpose) BETWEEN 1 AND 80 AND purpose = btrim(purpose)
    ),
    CONSTRAINT dispatch_policy_decisions_template_chk CHECK (char_length(template_id) <= 256),
    CONSTRAINT dispatch_policy_decisions_lease_chk CHECK (lease_version IS NULL OR lease_version >= 1)
);

CREATE INDEX dispatch_policy_decisions_attempt_idx
    ON dispatch_policy_decisions (tenant_id, attempt_id, evaluated_at, id);

ALTER TABLE dispatch_policy_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE dispatch_policy_decisions FORCE ROW LEVEL SECURITY;
CREATE POLICY dispatch_policy_decisions_tenant ON dispatch_policy_decisions
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
REVOKE ALL ON dispatch_policy_decisions FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT ON dispatch_policy_decisions TO chatisp_app;

-- Opt-out cancels only queued proactive rows that share the pending id.
-- retrying becomes a definitive local failure. sending and uncertain stay.
-- Lock order matches attendance: conversations, then messages, then attempts.
-- The caller invokes this before updating channel_policy_pending, so the
-- pending row is not held while those locks are still being acquired.
-- Multiple rows are locked one id at a time, in id order.
-- +goose StatementBegin
CREATE FUNCTION channel_policy_lock_outbox(p_tenant uuid, p_contact uuid, p_inbox uuid, p_purpose text)
RETURNS void
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    v_conversation uuid;
    v_message uuid;
    v_attempt uuid;
BEGIN
    FOR v_conversation IN
        SELECT id
          FROM public.conversations
         WHERE tenant_id = p_tenant
           AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
           AND contact_id = p_contact
           AND inbox_id = p_inbox
         ORDER BY id
    LOOP
        PERFORM 1
          FROM public.conversations
         WHERE tenant_id = p_tenant
           AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
           AND id = v_conversation
         FOR UPDATE;
    END LOOP;
    FOR v_message IN
        SELECT m.id
          FROM public.channel_policy_pending AS pending
          JOIN public.outbound_attempts AS attempt
            ON attempt.tenant_id = pending.tenant_id
           AND attempt.id = pending.id
          JOIN public.messages AS m
            ON m.tenant_id = attempt.tenant_id
           AND m.id = attempt.message_id
         WHERE pending.tenant_id = p_tenant
           AND pending.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
           AND pending.contact_id = p_contact
           AND pending.inbox_id = p_inbox
           AND pending.purpose = p_purpose
           AND pending.state = 'pending'
           AND attempt.state IN ('queued', 'retrying')
         ORDER BY m.id
    LOOP
        PERFORM 1
          FROM public.messages
         WHERE tenant_id = p_tenant
           AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
           AND id = v_message
         FOR UPDATE;
    END LOOP;
    FOR v_attempt IN
        SELECT attempt.id
          FROM public.channel_policy_pending AS pending
          JOIN public.outbound_attempts AS attempt
            ON attempt.tenant_id = pending.tenant_id
           AND attempt.id = pending.id
         WHERE pending.tenant_id = p_tenant
           AND pending.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
           AND pending.contact_id = p_contact
           AND pending.inbox_id = p_inbox
           AND pending.purpose = p_purpose
           AND pending.state = 'pending'
           AND attempt.state IN ('queued', 'retrying')
         ORDER BY attempt.id
    LOOP
        PERFORM 1
          FROM public.outbound_attempts
         WHERE tenant_id = p_tenant
           AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
           AND id = v_attempt
         FOR UPDATE;
    END LOOP;
END;
$$;
-- +goose StatementEnd

-- The trigger runs only after pending is updated. Attempts and messages are
-- already locked by channel_policy_lock_outbox in the same transaction.
-- The cursor is published after those rows, once per changed attempt.
-- +goose StatementBegin
CREATE FUNCTION channel_policy_cancel_outbox()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    v_message uuid;
    v_conversation uuid;
    v_state text;
    v_version bigint;
    v_seq bigint;
BEGIN
    IF NEW.state IS DISTINCT FROM 'cancelled' OR OLD.state IS DISTINCT FROM 'pending' THEN
        RETURN NEW;
    END IF;
    UPDATE public.outbound_attempts
       SET state = 'cancelled',
           error_code = 'consent_revoked',
           error_message = 'consentimento revogado antes do despacho'
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.id
       AND state = 'queued'
    RETURNING message_id, conversation_id, state
         INTO v_message, v_conversation, v_state;
    IF NOT FOUND THEN
        UPDATE public.outbound_attempts
           SET state = 'failed',
               error_code = 'consent_revoked',
               error_message = 'consentimento revogado antes do despacho'
         WHERE tenant_id = NEW.tenant_id
           AND id = NEW.id
           AND state = 'retrying'
        RETURNING message_id, conversation_id, state
             INTO v_message, v_conversation, v_state;
    END IF;
    IF NOT FOUND THEN
        RETURN NEW;
    END IF;
    UPDATE public.messages
       SET local_state = v_state
     WHERE tenant_id = NEW.tenant_id
       AND id = v_message;
    SELECT version
      INTO v_version
      FROM public.conversations
     WHERE tenant_id = NEW.tenant_id
       AND id = v_conversation;
    IF v_version IS NULL OR v_version < 1 THEN
        RAISE EXCEPTION 'conversa sem versão para o evento de revogação';
    END IF;
    INSERT INTO public.realtime_tenant_cursors (tenant_id, seq, retain_after)
    VALUES (NEW.tenant_id, 0, 0)
    ON CONFLICT (tenant_id) DO NOTHING;
    UPDATE public.realtime_tenant_cursors
       SET seq = seq + 1
     WHERE tenant_id = NEW.tenant_id
       AND tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    RETURNING seq INTO v_seq;
    IF v_seq IS NULL THEN
        RAISE EXCEPTION 'cursor de tempo real indisponível';
    END IF;
    INSERT INTO public.realtime_events (
        tenant_id, seq, id, event_type, aggregate_type, aggregate_id, version, payload
    ) VALUES (
        NEW.tenant_id,
        v_seq,
        gen_random_uuid(),
        'message.attempt',
        'conversation',
        v_conversation,
        v_version,
        jsonb_build_object(
            'conversation_id', v_conversation::text,
            'message_id', v_message::text,
            'attempt_id', NEW.id::text,
            'state', v_state
        )
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER channel_policy_cancel_outbox
    AFTER UPDATE OF state ON channel_policy_pending
    FOR EACH ROW
    EXECUTE FUNCTION channel_policy_cancel_outbox();

-- Mirror outbox transitions onto the proactive bridge without touching an
-- uncertain row except when a later receipt dispatches it.
-- +goose StatementBegin
CREATE FUNCTION outbound_policy_mirror()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF NEW.state IS NOT DISTINCT FROM OLD.state THEN
        RETURN NEW;
    END IF;
    UPDATE public.channel_policy_pending
       SET state = CASE NEW.state
           WHEN 'accepted' THEN 'dispatched'
           WHEN 'uncertain' THEN 'uncertain'
           ELSE 'cancelled'
       END
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.id
       AND (
           (state = 'pending' AND NEW.state IN ('accepted', 'uncertain', 'cancelled', 'failed'))
           OR (state = 'uncertain' AND NEW.state = 'accepted')
       );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER outbound_policy_mirror
    AFTER UPDATE OF state ON outbound_attempts
    FOR EACH ROW
    EXECUTE FUNCTION outbound_policy_mirror();

REVOKE ALL ON FUNCTION channel_policy_lock_outbox(uuid, uuid, uuid, text) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION channel_policy_cancel_outbox() FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION outbound_policy_mirror() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION channel_policy_lock_outbox(uuid, uuid, uuid, text) TO chatisp_app;
GRANT EXECUTE ON FUNCTION channel_policy_cancel_outbox() TO chatisp_app;
GRANT EXECUTE ON FUNCTION outbound_policy_mirror() TO chatisp_app;

-- +goose Down
DROP TRIGGER IF EXISTS outbound_policy_mirror ON outbound_attempts;
DROP FUNCTION IF EXISTS outbound_policy_mirror();
DROP TRIGGER IF EXISTS channel_policy_cancel_outbox ON channel_policy_pending;
DROP FUNCTION IF EXISTS channel_policy_cancel_outbox();
DROP FUNCTION IF EXISTS channel_policy_lock_outbox(uuid, uuid, uuid, text);
DROP TABLE IF EXISTS dispatch_policy_decisions;
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_content_context_chk;
ALTER TABLE messages DROP CONSTRAINT IF EXISTS messages_content_kind_chk;
ALTER TABLE messages DROP COLUMN IF EXISTS content_context;
ALTER TABLE messages DROP COLUMN IF EXISTS content_kind;
