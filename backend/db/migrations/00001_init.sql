-- +goose Up
-- Initial tenant boundary: registry, global users, memberships and audit.
-- Tenant-scoped tables use composite keys and forced row security.
-- Global tables stay without tenant_id and are granted by purpose.

CREATE TABLE tenants (
    id uuid PRIMARY KEY,
    timezone text NOT NULL CHECK (btrim(timezone) <> ''),
    status text NOT NULL CHECK (status IN ('active', 'suspended')),
    limits jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(limits) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id uuid PRIMARY KEY,
    email text NOT NULL CHECK (btrim(email) <> ''),
    password_hash text,
    status text NOT NULL CHECK (status IN ('invited', 'active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));

CREATE TABLE memberships (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    user_id uuid NOT NULL,
    role text NOT NULL CHECK (role IN ('admin', 'supervisor', 'agent')),
    status text NOT NULL CHECK (status IN ('invited', 'active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT memberships_tenant_user_key UNIQUE (tenant_id, user_id),
    CONSTRAINT memberships_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT memberships_user_fkey FOREIGN KEY (user_id) REFERENCES users (id)
);

CREATE TABLE audit_logs (
    id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    membership_id uuid,
    actor_user_id uuid,
    action text NOT NULL CHECK (btrim(action) <> ''),
    target_type text NOT NULL CHECK (btrim(target_type) <> ''),
    target_id uuid,
    result text NOT NULL CHECK (btrim(result) <> ''),
    correlation_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT audit_logs_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT audit_logs_membership_fkey
        FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships (tenant_id, id),
    CONSTRAINT audit_logs_actor_user_fkey FOREIGN KEY (actor_user_id) REFERENCES users (id)
);

CREATE INDEX audit_logs_tenant_created_idx ON audit_logs (tenant_id, created_at);

CREATE TABLE platform_audit_logs (
    id uuid PRIMARY KEY,
    actor text NOT NULL CHECK (btrim(actor) <> ''),
    action text NOT NULL CHECK (btrim(action) <> ''),
    target_type text NOT NULL CHECK (btrim(target_type) <> ''),
    target_id text,
    result text NOT NULL CHECK (btrim(result) <> ''),
    correlation_id text,
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE tenants ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenants FORCE ROW LEVEL SECURITY;
ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
ALTER TABLE memberships FORCE ROW LEVEL SECURITY;
ALTER TABLE audit_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_logs FORCE ROW LEVEL SECURITY;

-- Missing or blank app.tenant_id compares as NULL and matches nothing.
CREATE POLICY tenants_app_isolation ON tenants
    FOR ALL
    TO chatisp_app
    USING (id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY tenants_platform_scope ON tenants
    FOR ALL
    TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform')
    WITH CHECK (current_setting('app.access_scope', true) = 'platform');

CREATE POLICY memberships_app_isolation ON memberships
    FOR ALL
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY memberships_platform_scope ON memberships
    FOR ALL
    TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform')
    WITH CHECK (current_setting('app.access_scope', true) = 'platform');

CREATE POLICY audit_logs_app_isolation ON audit_logs
    FOR ALL
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT ON tenants TO chatisp_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON tenants TO chatisp_platform;

-- users is global operational metadata for the platform role.
-- password_hash is not granted: authentication uses the functions below.
REVOKE ALL ON users FROM chatisp_platform;
GRANT SELECT (id, email, status, created_at) ON users TO chatisp_platform;
GRANT INSERT (id, email, status) ON users TO chatisp_platform;
GRANT UPDATE (email, status) ON users TO chatisp_platform;
GRANT DELETE ON users TO chatisp_platform;

GRANT SELECT, INSERT, UPDATE, DELETE ON memberships TO chatisp_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON memberships TO chatisp_platform;

GRANT SELECT, INSERT, UPDATE, DELETE ON audit_logs TO chatisp_app;

GRANT SELECT, INSERT, UPDATE, DELETE ON platform_audit_logs TO chatisp_platform;

-- Restricted authentication path. The owner executes the body, search_path
-- cannot be hijacked, and PUBLIC/platform/ingress cannot call the functions.
-- The caller must set app.access_scope to auth inside the transaction.
-- +goose StatementBegin
CREATE FUNCTION auth_set_password_hash(p_user_id uuid, p_password_hash text)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    IF pg_catalog.current_setting('app.access_scope', true) IS DISTINCT FROM 'auth' THEN
        RAISE EXCEPTION 'contexto de autenticação ausente'
            USING ERRCODE = '42501';
    END IF;
    IF p_user_id IS NULL
        OR p_password_hash IS NULL
        OR pg_catalog.btrim(p_password_hash) = ''
        OR pg_catalog.length(p_password_hash) > 512
        OR p_password_hash <> pg_catalog.btrim(p_password_hash)
    THEN
        RAISE EXCEPTION 'hash de senha inválido'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.users
       SET password_hash = p_password_hash
     WHERE id = p_user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'usuário não encontrado'
            USING ERRCODE = 'P0002';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_password_hash_by_email(p_email text)
RETURNS text
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_hash text;
BEGIN
    IF pg_catalog.current_setting('app.access_scope', true) IS DISTINCT FROM 'auth' THEN
        RAISE EXCEPTION 'contexto de autenticação ausente'
            USING ERRCODE = '42501';
    END IF;
    IF p_email IS NULL
        OR pg_catalog.btrim(p_email) = ''
        OR pg_catalog.length(p_email) > 320
    THEN
        RAISE EXCEPTION 'e-mail inválido'
            USING ERRCODE = '22023';
    END IF;
    SELECT u.password_hash
      INTO v_hash
      FROM public.users AS u
     WHERE pg_catalog.lower(u.email) = pg_catalog.lower(pg_catalog.btrim(p_email));
    RETURN v_hash;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION auth_set_password_hash(uuid, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_password_hash_by_email(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION auth_set_password_hash(uuid, text) FROM chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_password_hash_by_email(text) FROM chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION auth_set_password_hash(uuid, text) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_password_hash_by_email(text) TO chatisp_app;

-- +goose Down
DROP FUNCTION IF EXISTS auth_set_password_hash(uuid, text);
DROP FUNCTION IF EXISTS auth_password_hash_by_email(text);
DROP TABLE IF EXISTS platform_audit_logs;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS memberships;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
