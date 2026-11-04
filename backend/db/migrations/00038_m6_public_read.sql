-- +goose Up
-- Exclusive read credential for the public API. This is not api_tokens:
-- that table belongs to migration 00031, owned by another block.
-- The raw secret is never stored. Lookup is by SHA-256, the same image
-- the panel uses for session secrets, through a security definer function.
-- chatisp_app cannot read the table. The owner can see tenants only while
-- the transaction scope is public_read, the same idea as the auth scope.

CREATE TABLE public_read_tokens (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    secret_hash bytea NOT NULL UNIQUE,
    scope text NOT NULL,
    revoked_at timestamptz,
    request_limit integer NOT NULL DEFAULT 60,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT public_read_tokens_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT public_read_tokens_hash_len CHECK (octet_length(secret_hash) = 32),
    CONSTRAINT public_read_tokens_scope_chk CHECK (scope = 'read'),
    CONSTRAINT public_read_tokens_limit_chk CHECK (request_limit BETWEEN 1 AND 6000)
);

CREATE INDEX public_read_tokens_tenant_idx ON public_read_tokens (tenant_id);

REVOKE ALL ON TABLE public_read_tokens FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;

CREATE POLICY tenants_owner_public_read ON tenants
    FOR SELECT
    TO chatisp_owner
    USING (current_setting('app.access_scope', true) = 'public_read');

-- +goose StatementBegin
CREATE FUNCTION public_read_require_scope()
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    IF pg_catalog.current_setting('app.access_scope', true) IS DISTINCT FROM 'public_read' THEN
        RAISE EXCEPTION 'contexto de leitura pública ausente'
            USING ERRCODE = '42501';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public_read_issue(
    p_id uuid,
    p_tenant_id uuid,
    p_secret_hash bytea,
    p_request_limit integer
)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.public_read_require_scope();
    IF p_id IS NULL
        OR p_tenant_id IS NULL
        OR p_secret_hash IS NULL
        OR pg_catalog.octet_length(p_secret_hash) <> 32
        OR p_request_limit IS NULL
        OR p_request_limit < 1
        OR p_request_limit > 6000
    THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;
    IF NOT EXISTS (
        SELECT 1
          FROM public.tenants
         WHERE id = p_tenant_id
           AND status IN ('active', 'suspended')
    ) THEN
        RAISE EXCEPTION 'provedor inexistente'
            USING ERRCODE = 'P0002';
    END IF;
    INSERT INTO public.public_read_tokens (id, tenant_id, secret_hash, scope, request_limit)
    VALUES (p_id, p_tenant_id, p_secret_hash, 'read', p_request_limit);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public_read_revoke(p_id uuid, p_at timestamptz)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_found boolean;
BEGIN
    PERFORM public.public_read_require_scope();
    IF p_id IS NULL OR p_at IS NULL THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.public_read_tokens
       SET revoked_at = p_at
     WHERE id = p_id
       AND revoked_at IS NULL;
    IF FOUND THEN
        RETURN;
    END IF;
    SELECT true INTO v_found FROM public.public_read_tokens WHERE id = p_id;
    IF NOT COALESCE(v_found, false) THEN
        RAISE EXCEPTION 'token inexistente'
            USING ERRCODE = 'P0002';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION public_read_resolve(p_secret_hash bytea)
RETURNS TABLE (
    token_id uuid,
    tenant_id uuid,
    scope text,
    revoked boolean,
    request_limit integer,
    tenant_status text
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.public_read_require_scope();
    IF p_secret_hash IS NULL OR pg_catalog.octet_length(p_secret_hash) <> 32 THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;
    RETURN QUERY
    SELECT token.id,
           token.tenant_id,
           token.scope,
           token.revoked_at IS NOT NULL,
           token.request_limit,
           tenant.status
      FROM public.public_read_tokens AS token
      JOIN public.tenants AS tenant ON tenant.id = token.tenant_id
     WHERE token.secret_hash = p_secret_hash;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION public_read_require_scope() FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION public_read_issue(uuid, uuid, bytea, integer) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION public_read_revoke(uuid, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION public_read_resolve(bytea) FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION public_read_issue(uuid, uuid, bytea, integer) TO chatisp_app;
GRANT EXECUTE ON FUNCTION public_read_revoke(uuid, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION public_read_resolve(bytea) TO chatisp_app;

-- +goose Down
DROP FUNCTION IF EXISTS public_read_resolve(bytea);
DROP FUNCTION IF EXISTS public_read_revoke(uuid, timestamptz);
DROP FUNCTION IF EXISTS public_read_issue(uuid, uuid, bytea, integer);
DROP FUNCTION IF EXISTS public_read_require_scope();
DROP POLICY IF EXISTS tenants_owner_public_read ON tenants;
DROP TABLE IF EXISTS public_read_tokens;
