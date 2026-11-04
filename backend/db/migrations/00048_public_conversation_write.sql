-- +goose Up
-- Escopo de escrita da API pública. Não cria tabela. O texto, a atribuição
-- e o estado reutilizam o atendimento já existente.

ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scopes_chk;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scopes_chk CHECK (
    cardinality(scopes) >= 1
    AND scopes <@ ARRAY[
        'read',
        'contacts.read',
        'contacts.write',
        'conversations.read',
        'conversations.write',
        'messages.read',
        'templates.send',
        'directory.read'
    ]::text[]
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION api_token_issue(
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
            'conversations.write',
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

-- +goose Down
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scopes_chk;
ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scopes_chk CHECK (
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
);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION api_token_issue(
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
