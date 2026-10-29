-- +goose Up
-- Provider administration: suspend or reactivate without deleting rows,
-- drop sessions of a suspended tenant or a disabled membership, and keep
-- attendance rows unreadable by the platform role.

CREATE TABLE conversations (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    summary text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT conversations_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT conversations_summary_len CHECK (char_length(summary) <= 4000)
);

ALTER TABLE conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversations FORCE ROW LEVEL SECURITY;

CREATE POLICY conversations_app_isolation ON conversations
    FOR ALL
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE conversations FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT ON conversations TO chatisp_app;

-- +goose StatementBegin
CREATE FUNCTION auth_revoke_tenant_sessions(p_tenant_id uuid, p_now timestamptz)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_tenant_id IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION 'sessão inválida'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.sessions
       SET revoked_at = p_now
     WHERE tenant_id = p_tenant_id
       AND revoked_at IS NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_revoke_membership_sessions(p_tenant_id uuid, p_user_id uuid, p_now timestamptz)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_tenant_id IS NULL OR p_user_id IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION 'sessão inválida'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.sessions
       SET revoked_at = p_now
     WHERE tenant_id = p_tenant_id
       AND user_id = p_user_id
       AND revoked_at IS NULL;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION auth_revoke_tenant_sessions(uuid, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_revoke_membership_sessions(uuid, uuid, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION auth_revoke_tenant_sessions(uuid, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_revoke_membership_sessions(uuid, uuid, timestamptz) TO chatisp_app;

-- +goose Down
DROP FUNCTION IF EXISTS auth_revoke_membership_sessions(uuid, uuid, timestamptz);
DROP FUNCTION IF EXISTS auth_revoke_tenant_sessions(uuid, timestamptz);
DROP TABLE IF EXISTS conversations;
