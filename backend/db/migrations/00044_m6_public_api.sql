-- +goose Up
-- Public API tokens (hash only) and the transactional source of outbound
-- webhook events. Delivery rows stay in webhook_deliveries. A missing River
-- schema keeps the source with enqueued_at null. webhook_recover_sources
-- inserts the job and marks the source in one transaction.

ALTER TABLE contacts ADD COLUMN external_ref text;
ALTER TABLE contacts ADD CONSTRAINT contacts_external_ref_chk CHECK (
    external_ref IS NULL OR (
        char_length(external_ref) BETWEEN 1 AND 128
        AND external_ref = btrim(external_ref)
        AND external_ref !~ '[[:space:]]'
    )
);
CREATE UNIQUE INDEX contacts_external_ref_key
    ON contacts (tenant_id, external_ref)
    WHERE external_ref IS NOT NULL;

CREATE TABLE api_tokens (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    secret_hash bytea NOT NULL UNIQUE,
    scopes text[] NOT NULL,
    request_limit integer NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_tokens_hash_len CHECK (octet_length(secret_hash) = 32),
    CONSTRAINT api_tokens_limit_chk CHECK (request_limit BETWEEN 1 AND 6000),
    CONSTRAINT api_tokens_scopes_chk CHECK (
        cardinality(scopes) >= 1
        AND scopes <@ ARRAY[
            'read',
            'contacts.read',
            'contacts.write',
            'conversations.read',
            'messages.read',
            'templates.send',
            'directory.read'
        ]::text[]
    )
);

REVOKE ALL ON TABLE api_tokens FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;

-- The issuer runs as the table owner. FORCE RLS hides tenants unless this
-- transaction explicitly opens the owner read used only by token issuance.
CREATE POLICY tenants_owner_api_token ON tenants
    FOR SELECT
    TO chatisp_owner
    USING (current_setting('app.api_token', true) = 'on');

CREATE TABLE webhook_event_sources (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    event_type text NOT NULL,
    source_id uuid NOT NULL,
    conversation_id uuid NOT NULL,
    occurred_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    enqueued_at timestamptz,
    PRIMARY KEY (tenant_id, event_type, source_id),
    CONSTRAINT webhook_event_sources_type_chk CHECK (
        event_type IN (
            'conversation.created',
            'conversation.assigned',
            'conversation.closed',
            'message.created'
        )
    )
);

ALTER TABLE webhook_event_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_event_sources FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON webhook_event_sources
    FOR ALL
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY owner_maintenance ON webhook_event_sources
    FOR ALL
    TO chatisp_owner
    USING (true)
    WITH CHECK (true);

REVOKE ALL ON TABLE webhook_event_sources FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT ON webhook_event_sources TO chatisp_app;

CREATE INDEX webhook_event_sources_pending_idx
    ON webhook_event_sources (created_at, tenant_id, event_type, source_id)
    WHERE enqueued_at IS NULL;

CREATE TABLE api_template_requests (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    token_id uuid NOT NULL,
    client_request_id text NOT NULL,
    conversation_id uuid NOT NULL,
    payload_hash bytea NOT NULL,
    membership_id uuid NOT NULL,
    message_id uuid,
    attempt_id uuid,
    job_id bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    PRIMARY KEY (tenant_id, token_id, client_request_id),
    CONSTRAINT api_template_requests_id_chk CHECK (
        char_length(client_request_id) BETWEEN 1 AND 200
        AND client_request_id = btrim(client_request_id)
    ),
    CONSTRAINT api_template_requests_hash_chk CHECK (octet_length(payload_hash) = 32),
    CONSTRAINT api_template_requests_done_chk CHECK (
        (
            message_id IS NULL AND attempt_id IS NULL AND job_id IS NULL AND completed_at IS NULL
        ) OR (
            message_id IS NOT NULL AND attempt_id IS NOT NULL AND job_id IS NOT NULL AND completed_at IS NOT NULL
        )
    )
);

ALTER TABLE api_template_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE api_template_requests FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON api_template_requests
    FOR ALL
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE api_template_requests FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT, UPDATE ON api_template_requests TO chatisp_app;

-- The public template sender records the River job id in the same transaction
-- as the outbox row. Recovery reads it when the public result row is still open.
CREATE TABLE outbound_job_links (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    attempt_id uuid NOT NULL,
    job_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, attempt_id),
    CONSTRAINT outbound_job_links_job_chk CHECK (job_id > 0)
);

ALTER TABLE outbound_job_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE outbound_job_links FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON outbound_job_links
    FOR ALL
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY owner_maintenance ON outbound_job_links
    FOR ALL
    TO chatisp_owner
    USING (true)
    WITH CHECK (true);

REVOKE ALL ON TABLE outbound_job_links FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT ON outbound_job_links TO chatisp_app;

ALTER TABLE webhook_deliveries ADD COLUMN claim_token uuid;
ALTER TABLE webhook_deliveries ADD COLUMN claimed_at timestamptz;
ALTER TABLE webhook_deliveries ADD CONSTRAINT webhook_deliveries_claim_chk CHECK (
    (claim_token IS NULL AND claimed_at IS NULL)
    OR (claim_token IS NOT NULL AND claimed_at IS NOT NULL)
);

-- +goose StatementBegin
CREATE FUNCTION api_token_issue(
    p_id uuid,
    p_tenant_id uuid,
    p_secret_hash bytea,
    p_scopes text[],
    p_request_limit integer
)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    IF pg_catalog.current_setting('app.tenant_id', true) IS DISTINCT FROM p_tenant_id::text THEN
        RAISE EXCEPTION 'provedor ausente'
            USING ERRCODE = '42501';
    END IF;
    IF p_id IS NULL
        OR p_tenant_id IS NULL
        OR p_secret_hash IS NULL
        OR pg_catalog.octet_length(p_secret_hash) <> 32
        OR p_scopes IS NULL
        OR pg_catalog.cardinality(p_scopes) < 1
        OR NOT (p_scopes <@ ARRAY[
            'read',
            'contacts.read',
            'contacts.write',
            'conversations.read',
            'messages.read',
            'templates.send',
            'directory.read'
        ]::text[])
        OR p_request_limit IS NULL
        OR p_request_limit < 1
        OR p_request_limit > 6000
    THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;
    PERFORM pg_catalog.set_config('app.api_token', 'on', true);
    IF NOT EXISTS (
        SELECT 1 FROM public.tenants WHERE id = p_tenant_id AND status IN ('active', 'suspended')
    ) THEN
        RAISE EXCEPTION 'provedor inexistente'
            USING ERRCODE = 'P0002';
    END IF;
    INSERT INTO public.api_tokens (id, tenant_id, secret_hash, scopes, request_limit)
    VALUES (p_id, p_tenant_id, p_secret_hash, p_scopes, p_request_limit);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION api_token_revoke(p_id uuid, p_at timestamptz)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_tenant uuid;
BEGIN
    IF p_id IS NULL OR p_at IS NULL THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;
    SELECT tenant_id INTO v_tenant FROM public.api_tokens WHERE id = p_id;
    IF v_tenant IS NULL THEN
        RAISE EXCEPTION 'token inexistente'
            USING ERRCODE = 'P0002';
    END IF;
    IF pg_catalog.current_setting('app.tenant_id', true) IS DISTINCT FROM v_tenant::text THEN
        RAISE EXCEPTION 'provedor ausente'
            USING ERRCODE = '42501';
    END IF;
    UPDATE public.api_tokens
       SET revoked_at = p_at
     WHERE id = p_id
       AND revoked_at IS NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION api_token_list()
RETURNS TABLE (
    id uuid,
    scopes text[],
    request_limit integer,
    revoked boolean,
    created_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_tenant uuid;
BEGIN
    BEGIN
        v_tenant := pg_catalog.current_setting('app.tenant_id', true)::uuid;
    EXCEPTION WHEN invalid_text_representation OR OTHERS THEN
        RAISE EXCEPTION 'provedor ausente'
            USING ERRCODE = '42501';
    END;
    IF v_tenant IS NULL THEN
        RAISE EXCEPTION 'provedor ausente'
            USING ERRCODE = '42501';
    END IF;
    RETURN QUERY
    SELECT token.id, token.scopes, token.request_limit, token.revoked_at IS NOT NULL, token.created_at
      FROM public.api_tokens AS token
     WHERE token.tenant_id = v_tenant
     ORDER BY token.created_at, token.id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION api_token_resolve(p_secret_hash bytea)
RETURNS TABLE (
    token_id uuid,
    tenant_id uuid,
    scopes text[],
    revoked boolean,
    request_limit integer,
    tenant_status text
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    IF pg_catalog.current_setting('app.access_scope', true) IS DISTINCT FROM 'public_read' THEN
        RAISE EXCEPTION 'contexto de leitura pública ausente'
            USING ERRCODE = '42501';
    END IF;
    IF p_secret_hash IS NULL OR pg_catalog.octet_length(p_secret_hash) <> 32 THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;
    RETURN QUERY
    SELECT token.id,
           token.tenant_id,
           token.scopes,
           token.revoked_at IS NOT NULL,
           token.request_limit,
           tenant.status
      FROM public.api_tokens AS token
      JOIN public.tenants AS tenant ON tenant.id = token.tenant_id
     WHERE token.secret_hash = p_secret_hash;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION api_token_issue(uuid, uuid, bytea, text[], integer) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION api_token_revoke(uuid, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION api_token_list() FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION api_token_resolve(bytea) FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION api_token_issue(uuid, uuid, bytea, text[], integer) TO chatisp_app;
GRANT EXECUTE ON FUNCTION api_token_revoke(uuid, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION api_token_list() TO chatisp_app;
GRANT EXECUTE ON FUNCTION api_token_resolve(bytea) TO chatisp_app;

-- +goose StatementBegin
CREATE FUNCTION webhook_capture()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
DECLARE
    v_type text;
    v_source uuid;
    v_at timestamptz;
BEGIN
    IF TG_TABLE_NAME = 'conversations' AND TG_OP = 'INSERT' THEN
        v_type := 'conversation.created';
        v_source := NEW.id;
        v_at := COALESCE(NEW.created_at, pg_catalog.now());
        INSERT INTO public.webhook_event_sources (tenant_id, event_type, source_id, conversation_id, occurred_at)
        VALUES (NEW.tenant_id, v_type, v_source, NEW.id, v_at)
        ON CONFLICT DO NOTHING;
        PERFORM public.webhook_enqueue(NEW.tenant_id, v_type, v_source);
        RETURN NULL;
    END IF;
    IF TG_TABLE_NAME = 'conversations' AND TG_OP = 'UPDATE' THEN
        -- Each real assignee or close change is its own source. A retry of the
        -- same committed change does not update the row again, so it does not
        -- insert another source. The random id is stable once this transaction
        -- commits and is what River retries.
        IF NEW.assignee_id IS NOT NULL AND NEW.assignee_id IS DISTINCT FROM OLD.assignee_id THEN
            v_source := pg_catalog.gen_random_uuid();
            v_at := pg_catalog.now();
            INSERT INTO public.webhook_event_sources (tenant_id, event_type, source_id, conversation_id, occurred_at)
            VALUES (NEW.tenant_id, 'conversation.assigned', v_source, NEW.id, v_at)
            ON CONFLICT DO NOTHING;
            PERFORM public.webhook_enqueue(NEW.tenant_id, 'conversation.assigned', v_source);
        END IF;
        IF NEW.status = 'closed' AND OLD.status IS DISTINCT FROM 'closed' THEN
            v_source := pg_catalog.gen_random_uuid();
            v_at := pg_catalog.now();
            INSERT INTO public.webhook_event_sources (tenant_id, event_type, source_id, conversation_id, occurred_at)
            VALUES (NEW.tenant_id, 'conversation.closed', v_source, NEW.id, v_at)
            ON CONFLICT DO NOTHING;
            PERFORM public.webhook_enqueue(NEW.tenant_id, 'conversation.closed', v_source);
        END IF;
        RETURN NULL;
    END IF;
    IF TG_TABLE_NAME = 'messages' AND TG_OP = 'INSERT' THEN
        v_at := COALESCE(NEW.ingested_at, pg_catalog.now());
        INSERT INTO public.webhook_event_sources (tenant_id, event_type, source_id, conversation_id, occurred_at)
        VALUES (NEW.tenant_id, 'message.created', NEW.id, NEW.conversation_id, v_at)
        ON CONFLICT DO NOTHING;
        PERFORM public.webhook_enqueue(NEW.tenant_id, 'message.created', NEW.id);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- webhook_enqueue inserts one River job and marks the source. The unique key
-- blocks a second active job for the same origin. A missing River schema leaves
-- enqueued_at null so recovery can retry. An origin already marked is a no-op,
-- including when the capture insert conflicted.
-- +goose StatementBegin
CREATE FUNCTION webhook_enqueue(p_tenant uuid, p_type text, p_source uuid)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_key bytea;
BEGIN
    IF p_tenant IS NULL OR p_type IS NULL OR p_source IS NULL THEN
        RAISE EXCEPTION 'origem inválida'
            USING ERRCODE = '22023';
    END IF;
    -- chatisp_app must carry the source tenant. The table owner and a bootstrap
    -- superuser (migration and disposable tests) may write the conversation
    -- before app.tenant_id exists. Other roles keep the same refusal. This does
    -- not grant them the function; EXECUTE stays with app and owner.
    IF session_user <> 'chatisp_owner'
        AND NOT EXISTS (
            SELECT 1
              FROM pg_catalog.pg_roles
             WHERE rolname = session_user
               AND rolsuper
        )
        AND pg_catalog.current_setting('app.tenant_id', true) IS DISTINCT FROM p_tenant::text
    THEN
        RAISE EXCEPTION 'provedor ausente'
            USING ERRCODE = '42501';
    END IF;
    IF NOT EXISTS (
        SELECT 1
          FROM public.webhook_event_sources
         WHERE tenant_id = p_tenant
           AND event_type = p_type
           AND source_id = p_source
           AND enqueued_at IS NULL
    ) THEN
        RETURN;
    END IF;
    v_key := pg_catalog.decode(pg_catalog.md5(p_tenant::text || '|' || p_type || '|' || p_source::text), 'hex');
    BEGIN
        INSERT INTO river.river_job (args, kind, max_attempts, metadata, queue, unique_key, unique_states)
        VALUES (
            pg_catalog.jsonb_build_object(
                'v', 1,
                'tenant_id', p_tenant,
                'event_type', p_type,
                'source_id', p_source
            ),
            'chatisp.webhook.deliver.v1',
            8,
            '{"v":1}'::jsonb,
            'webhooks',
            v_key,
            241::bit(8)
        );
        UPDATE public.webhook_event_sources
           SET enqueued_at = pg_catalog.now()
         WHERE tenant_id = p_tenant
           AND event_type = p_type
           AND source_id = p_source
           AND enqueued_at IS NULL;
    EXCEPTION
        WHEN unique_violation THEN
            UPDATE public.webhook_event_sources
               SET enqueued_at = pg_catalog.now()
             WHERE tenant_id = p_tenant
               AND event_type = p_type
               AND source_id = p_source
               AND enqueued_at IS NULL;
        WHEN undefined_table OR invalid_schema_name OR insufficient_privilege THEN
            NULL;
    END;
END;
$$;
-- +goose StatementEnd

-- Recovery locks unmarked sources and inserts the job in that same transaction.
-- It does not call webhook_enqueue, because that function only accepts the
-- tenant pinned on the caller session. SKIP LOCKED lets two workers split the
-- set. A source stays unmarked when River is still absent.
-- +goose StatementBegin
CREATE FUNCTION webhook_recover_sources()
RETURNS integer
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    r record;
    n integer := 0;
    v_key bytea;
BEGIN
    FOR r IN
        SELECT tenant_id, event_type, source_id
          FROM public.webhook_event_sources
         WHERE enqueued_at IS NULL
         ORDER BY created_at, tenant_id, event_type, source_id
         FOR UPDATE SKIP LOCKED
    LOOP
        v_key := pg_catalog.decode(pg_catalog.md5(r.tenant_id::text || '|' || r.event_type || '|' || r.source_id::text), 'hex');
        BEGIN
            INSERT INTO river.river_job (args, kind, max_attempts, metadata, queue, unique_key, unique_states)
            VALUES (
                pg_catalog.jsonb_build_object(
                    'v', 1,
                    'tenant_id', r.tenant_id,
                    'event_type', r.event_type,
                    'source_id', r.source_id
                ),
                'chatisp.webhook.deliver.v1',
                8,
                '{"v":1}'::jsonb,
                'webhooks',
                v_key,
                241::bit(8)
            );
            UPDATE public.webhook_event_sources
               SET enqueued_at = pg_catalog.now()
             WHERE tenant_id = r.tenant_id
               AND event_type = r.event_type
               AND source_id = r.source_id
               AND enqueued_at IS NULL;
            n := n + 1;
        EXCEPTION
            WHEN unique_violation THEN
                UPDATE public.webhook_event_sources
                   SET enqueued_at = pg_catalog.now()
                 WHERE tenant_id = r.tenant_id
                   AND event_type = r.event_type
                   AND source_id = r.source_id
                   AND enqueued_at IS NULL;
                n := n + 1;
            WHEN undefined_table OR invalid_schema_name OR insufficient_privilege THEN
                RETURN n;
        END;
    END LOOP;
    RETURN n;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION webhook_capture() FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION webhook_enqueue(uuid, text, uuid) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION webhook_recover_sources() FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION webhook_capture() TO chatisp_app, chatisp_owner;
GRANT EXECUTE ON FUNCTION webhook_enqueue(uuid, text, uuid) TO chatisp_app, chatisp_owner;
GRANT EXECUTE ON FUNCTION webhook_recover_sources() TO chatisp_app, chatisp_owner;

CREATE TRIGGER conversations_webhook_capture
    AFTER INSERT OR UPDATE OF assignee_id, status ON conversations
    FOR EACH ROW
    EXECUTE FUNCTION webhook_capture();

CREATE TRIGGER messages_webhook_capture
    AFTER INSERT ON messages
    FOR EACH ROW
    EXECUTE FUNCTION webhook_capture();

-- +goose Down
DROP TRIGGER IF EXISTS messages_webhook_capture ON messages;
DROP TRIGGER IF EXISTS conversations_webhook_capture ON conversations;
DROP FUNCTION IF EXISTS webhook_recover_sources();
DROP FUNCTION IF EXISTS webhook_capture();
DROP FUNCTION IF EXISTS webhook_enqueue(uuid, text, uuid);
DROP FUNCTION IF EXISTS api_token_resolve(bytea);
DROP FUNCTION IF EXISTS api_token_list();
DROP FUNCTION IF EXISTS api_token_revoke(uuid, timestamptz);
DROP FUNCTION IF EXISTS api_token_issue(uuid, uuid, bytea, text[], integer);
ALTER TABLE webhook_deliveries DROP CONSTRAINT IF EXISTS webhook_deliveries_claim_chk;
ALTER TABLE webhook_deliveries DROP COLUMN IF EXISTS claim_token;
ALTER TABLE webhook_deliveries DROP COLUMN IF EXISTS claimed_at;
DROP TABLE IF EXISTS outbound_job_links;
DROP TABLE IF EXISTS api_template_requests;
DROP TABLE IF EXISTS webhook_event_sources;
DROP TABLE IF EXISTS api_tokens;
DROP POLICY IF EXISTS tenants_owner_api_token ON tenants;
DROP INDEX IF EXISTS contacts_external_ref_key;
ALTER TABLE contacts DROP CONSTRAINT IF EXISTS contacts_external_ref_chk;
ALTER TABLE contacts DROP COLUMN IF EXISTS external_ref;
