-- +goose Up
-- Identity path: sessions, one-time tokens and login counters.
-- These tables are global. The application role cannot read them.
-- chatisp_app reaches them only through SECURITY DEFINER functions
-- while app.access_scope is auth. Session and token secrets are stored
-- as SHA-256 hashes; the raw values never land in the database.

CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE,
    user_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    membership_id uuid NOT NULL,
    csrf_hash bytea NOT NULL,
    created_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    idle_expires_at timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CONSTRAINT sessions_token_hash_len CHECK (octet_length(token_hash) = 32),
    CONSTRAINT sessions_csrf_hash_len CHECK (octet_length(csrf_hash) = 32),
    CONSTRAINT sessions_expiry_order CHECK (idle_expires_at <= absolute_expires_at),
    CONSTRAINT sessions_user_fkey FOREIGN KEY (user_id) REFERENCES users (id),
    CONSTRAINT sessions_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id),
    CONSTRAINT sessions_membership_fkey
        FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships (tenant_id, id)
);

CREATE INDEX sessions_user_active_idx ON sessions (user_id) WHERE revoked_at IS NULL;

CREATE TABLE auth_tokens (
    id uuid PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE,
    purpose text NOT NULL CHECK (purpose IN ('invite', 'password_reset')),
    user_id uuid NOT NULL,
    tenant_id uuid,
    membership_id uuid,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT auth_tokens_hash_len CHECK (octet_length(token_hash) = 32),
    CONSTRAINT auth_tokens_shape CHECK (
        (purpose = 'password_reset' AND tenant_id IS NULL AND membership_id IS NULL)
        OR (purpose = 'invite' AND tenant_id IS NOT NULL AND membership_id IS NOT NULL)
    ),
    CONSTRAINT auth_tokens_user_fkey FOREIGN KEY (user_id) REFERENCES users (id),
    CONSTRAINT auth_tokens_membership_fkey
        FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships (tenant_id, id)
);

CREATE INDEX auth_tokens_user_purpose_idx ON auth_tokens (user_id, purpose) WHERE used_at IS NULL;

CREATE TABLE auth_attempt_counters (
    kind text NOT NULL CHECK (kind IN ('login_account', 'login_ip', 'reset_account', 'reset_ip', 'token_ip')),
    subject_hash bytea NOT NULL,
    failures integer NOT NULL CHECK (failures >= 0),
    window_started_at timestamptz NOT NULL,
    locked_until timestamptz,
    PRIMARY KEY (kind, subject_hash),
    CONSTRAINT auth_attempt_counters_hash_len CHECK (octet_length(subject_hash) = 32)
);

REVOKE ALL ON TABLE sessions FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE auth_tokens FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE auth_attempt_counters FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;

-- Owner is subject to FORCE RLS. Auth functions run as owner and must see
-- memberships and tenants only while the caller opened an auth transaction.
CREATE POLICY tenants_owner_auth ON tenants
    FOR SELECT
    TO chatisp_owner
    USING (current_setting('app.access_scope', true) = 'auth');

CREATE POLICY memberships_owner_auth ON memberships
    FOR ALL
    TO chatisp_owner
    USING (current_setting('app.access_scope', true) = 'auth')
    WITH CHECK (current_setting('app.access_scope', true) = 'auth');

-- +goose StatementBegin
CREATE FUNCTION auth_require_scope()
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
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_login_identity(p_email text)
RETURNS TABLE (
    user_id uuid,
    email text,
    status text,
    password_hash text,
    memberships jsonb
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_email text;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_email IS NULL
        OR pg_catalog.btrim(p_email) = ''
        OR pg_catalog.length(p_email) > 320
    THEN
        RAISE EXCEPTION 'e-mail inválido'
            USING ERRCODE = '22023';
    END IF;
    v_email := pg_catalog.lower(pg_catalog.btrim(p_email));
    RETURN QUERY
    SELECT u.id,
           u.email,
           u.status,
           COALESCE(u.password_hash, ''),
           COALESCE((
               SELECT pg_catalog.jsonb_agg(pg_catalog.jsonb_build_object(
                   'tenant_id', m.tenant_id,
                   'membership_id', m.id,
                   'role', m.role,
                   'status', m.status,
                   'tenant_status', t.status
               ))
               FROM public.memberships AS m
               JOIN public.tenants AS t ON t.id = m.tenant_id
               WHERE m.user_id = u.id
           ), '[]'::jsonb)
      FROM public.users AS u
     WHERE pg_catalog.lower(u.email) = v_email;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_insert_session(
    p_id uuid,
    p_token_hash bytea,
    p_user_id uuid,
    p_tenant_id uuid,
    p_membership_id uuid,
    p_csrf_hash bytea,
    p_now timestamptz,
    p_idle_expires_at timestamptz,
    p_absolute_expires_at timestamptz
)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_user_status text;
    v_member_status text;
    v_tenant_status text;
    v_member_user uuid;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_id IS NULL
        OR p_user_id IS NULL
        OR p_tenant_id IS NULL
        OR p_membership_id IS NULL
        OR p_token_hash IS NULL
        OR pg_catalog.octet_length(p_token_hash) <> 32
        OR p_csrf_hash IS NULL
        OR pg_catalog.octet_length(p_csrf_hash) <> 32
        OR p_now IS NULL
        OR p_idle_expires_at IS NULL
        OR p_absolute_expires_at IS NULL
        OR p_idle_expires_at <= p_now
        OR p_absolute_expires_at <= p_now
        OR p_idle_expires_at > p_absolute_expires_at
    THEN
        RAISE EXCEPTION 'sessão inválida'
            USING ERRCODE = '22023';
    END IF;

    SELECT u.status INTO v_user_status FROM public.users AS u WHERE u.id = p_user_id;
    IF v_user_status IS DISTINCT FROM 'active' THEN
        RAISE EXCEPTION 'usuário não encontrado'
            USING ERRCODE = 'P0002';
    END IF;

    SELECT m.status, m.user_id
      INTO v_member_status, v_member_user
      FROM public.memberships AS m
     WHERE m.tenant_id = p_tenant_id
       AND m.id = p_membership_id;
    IF v_member_user IS DISTINCT FROM p_user_id OR v_member_status IS DISTINCT FROM 'active' THEN
        RAISE EXCEPTION 'usuário não encontrado'
            USING ERRCODE = 'P0002';
    END IF;

    SELECT t.status INTO v_tenant_status FROM public.tenants AS t WHERE t.id = p_tenant_id;
    IF v_tenant_status IS DISTINCT FROM 'active' THEN
        RAISE EXCEPTION 'usuário não encontrado'
            USING ERRCODE = 'P0002';
    END IF;

    INSERT INTO public.sessions (
        id, token_hash, user_id, tenant_id, membership_id, csrf_hash,
        created_at, last_seen_at, idle_expires_at, absolute_expires_at
    ) VALUES (
        p_id, p_token_hash, p_user_id, p_tenant_id, p_membership_id, p_csrf_hash,
        p_now, p_now, p_idle_expires_at, p_absolute_expires_at
    );
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_load_session(p_token_hash bytea, p_now timestamptz)
RETURNS TABLE (
    session_status text,
    session_id uuid,
    user_id uuid,
    tenant_id uuid,
    membership_id uuid,
    role text,
    user_status text,
    membership_status text,
    tenant_status text,
    email text,
    csrf_hash bytea,
    absolute_expires_at timestamptz,
    idle_expires_at timestamptz
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_session public.sessions%ROWTYPE;
    v_role text;
    v_user_status text;
    v_member_status text;
    v_tenant_status text;
    v_email text;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_token_hash IS NULL
        OR pg_catalog.octet_length(p_token_hash) <> 32
        OR p_now IS NULL
    THEN
        RAISE EXCEPTION 'sessão inválida'
            USING ERRCODE = '22023';
    END IF;

    SELECT * INTO v_session FROM public.sessions AS s WHERE s.token_hash = p_token_hash;
    IF NOT FOUND THEN
        session_status := 'missing';
        session_id := '00000000-0000-0000-0000-000000000000';
        user_id := '00000000-0000-0000-0000-000000000000';
        tenant_id := '00000000-0000-0000-0000-000000000000';
        membership_id := '00000000-0000-0000-0000-000000000000';
        role := '';
        user_status := '';
        membership_status := '';
        tenant_status := '';
        email := '';
        csrf_hash := ''::bytea;
        absolute_expires_at := p_now;
        idle_expires_at := p_now;
        RETURN NEXT;
        RETURN;
    END IF;

    session_id := v_session.id;
    user_id := v_session.user_id;
    tenant_id := v_session.tenant_id;
    membership_id := v_session.membership_id;
    csrf_hash := v_session.csrf_hash;
    absolute_expires_at := v_session.absolute_expires_at;
    idle_expires_at := v_session.idle_expires_at;

    SELECT u.email, u.status INTO v_email, v_user_status
      FROM public.users AS u WHERE u.id = v_session.user_id;
    SELECT m.role, m.status INTO v_role, v_member_status
      FROM public.memberships AS m
     WHERE m.tenant_id = v_session.tenant_id AND m.id = v_session.membership_id;
    SELECT t.status INTO v_tenant_status
      FROM public.tenants AS t WHERE t.id = v_session.tenant_id;

    role := COALESCE(v_role, '');
    user_status := COALESCE(v_user_status, '');
    membership_status := COALESCE(v_member_status, '');
    tenant_status := COALESCE(v_tenant_status, '');
    email := COALESCE(v_email, '');

    IF v_session.revoked_at IS NOT NULL THEN
        session_status := 'revoked';
    ELSIF v_session.absolute_expires_at <= p_now THEN
        session_status := 'expired';
    ELSIF v_session.idle_expires_at <= p_now THEN
        session_status := 'idle';
    ELSIF v_user_status IS DISTINCT FROM 'active'
        OR v_member_status IS DISTINCT FROM 'active'
        OR v_tenant_status IS DISTINCT FROM 'active' THEN
        session_status := 'blocked';
    ELSE
        session_status := 'active';
    END IF;
    RETURN NEXT;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_touch_session(p_id uuid, p_now timestamptz, p_idle_expires_at timestamptz)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_found boolean;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_id IS NULL OR p_now IS NULL OR p_idle_expires_at IS NULL OR p_idle_expires_at <= p_now THEN
        RAISE EXCEPTION 'sessão inválida'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.sessions
       SET last_seen_at = p_now,
           idle_expires_at = LEAST(p_idle_expires_at, absolute_expires_at)
     WHERE id = p_id
       AND revoked_at IS NULL
       AND absolute_expires_at > p_now
       AND idle_expires_at > p_now;
    v_found := FOUND;
    RETURN v_found;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_revoke_by_token_hash(p_token_hash bytea, p_now timestamptz)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_token_hash IS NULL OR pg_catalog.octet_length(p_token_hash) <> 32 OR p_now IS NULL THEN
        RAISE EXCEPTION 'sessão inválida'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.sessions
       SET revoked_at = p_now
     WHERE token_hash = p_token_hash
       AND revoked_at IS NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_revoke_user_sessions(p_user_id uuid, p_now timestamptz)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_user_id IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION 'sessão inválida'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.sessions
       SET revoked_at = p_now
     WHERE user_id = p_user_id
       AND revoked_at IS NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_revoke_session(
    p_session_id uuid,
    p_tenant_id uuid,
    p_actor uuid,
    p_now timestamptz
)
RETURNS text
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_user_id uuid;
    v_tenant_id uuid;
    v_role text;
    v_member_status text;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_session_id IS NULL OR p_tenant_id IS NULL OR p_actor IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION 'sessão inválida'
            USING ERRCODE = '22023';
    END IF;

    SELECT s.user_id, s.tenant_id
      INTO v_user_id, v_tenant_id
      FROM public.sessions AS s
     WHERE s.id = p_session_id
     FOR UPDATE;
    IF NOT FOUND THEN
        RETURN 'missing';
    END IF;

    SELECT m.role, m.status
      INTO v_role, v_member_status
      FROM public.memberships AS m
     WHERE m.tenant_id = p_tenant_id
       AND m.user_id = p_actor;

    IF NOT (
        (v_user_id = p_actor AND v_tenant_id = p_tenant_id)
        OR (
            v_tenant_id = p_tenant_id
            AND v_role = 'admin'
            AND v_member_status = 'active'
        )
    ) THEN
        RETURN 'forbidden';
    END IF;

    UPDATE public.sessions
       SET revoked_at = p_now
     WHERE id = p_session_id
       AND revoked_at IS NULL;
    RETURN 'ok';
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_list_tenant_sessions(p_tenant_id uuid, p_now timestamptz)
RETURNS TABLE (
    session_id uuid,
    user_id uuid,
    created_at timestamptz,
    last_seen_at timestamptz,
    absolute_expires_at timestamptz
)
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
    RETURN QUERY
    SELECT s.id, s.user_id, s.created_at, s.last_seen_at, s.absolute_expires_at
      FROM public.sessions AS s
     WHERE s.tenant_id = p_tenant_id
       AND s.revoked_at IS NULL
       AND s.absolute_expires_at > p_now
       AND s.idle_expires_at > p_now
     ORDER BY s.created_at, s.id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_insert_auth_token(
    p_id uuid,
    p_token_hash bytea,
    p_purpose text,
    p_user_id uuid,
    p_tenant_id uuid,
    p_membership_id uuid,
    p_expires_at timestamptz,
    p_now timestamptz
)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_id IS NULL
        OR p_user_id IS NULL
        OR p_token_hash IS NULL
        OR pg_catalog.octet_length(p_token_hash) <> 32
        OR p_purpose NOT IN ('invite', 'password_reset')
        OR p_expires_at IS NULL
        OR p_now IS NULL
        OR p_expires_at <= p_now
    THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;
    IF p_purpose = 'invite' AND (p_tenant_id IS NULL OR p_membership_id IS NULL) THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;
    IF p_purpose = 'password_reset' AND (p_tenant_id IS NOT NULL OR p_membership_id IS NOT NULL) THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;

    UPDATE public.auth_tokens
       SET used_at = p_now
     WHERE user_id = p_user_id
       AND purpose = p_purpose
       AND used_at IS NULL
       AND (
           (p_purpose = 'password_reset')
           OR (tenant_id = p_tenant_id AND membership_id = p_membership_id)
       );

    INSERT INTO public.auth_tokens (
        id, token_hash, purpose, user_id, tenant_id, membership_id, expires_at, created_at
    ) VALUES (
        p_id, p_token_hash, p_purpose, p_user_id, p_tenant_id, p_membership_id, p_expires_at, p_now
    );
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_invalidate_auth_token(p_token_hash bytea, p_now timestamptz)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_token_hash IS NULL OR pg_catalog.octet_length(p_token_hash) <> 32 OR p_now IS NULL THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.auth_tokens
       SET used_at = p_now
     WHERE token_hash = p_token_hash
       AND used_at IS NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_accept_invite(p_token_hash bytea, p_password_hash text, p_now timestamptz)
RETURNS TABLE (
    token_status text,
    user_id uuid,
    tenant_id uuid,
    membership_id uuid
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_token public.auth_tokens%ROWTYPE;
    v_user_status text;
    v_member_status text;
    v_tenant_status text;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_token_hash IS NULL
        OR pg_catalog.octet_length(p_token_hash) <> 32
        OR p_password_hash IS NULL
        OR pg_catalog.btrim(p_password_hash) = ''
        OR pg_catalog.length(p_password_hash) > 512
        OR p_password_hash <> pg_catalog.btrim(p_password_hash)
        OR p_now IS NULL
    THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;

    SELECT * INTO v_token
      FROM public.auth_tokens
     WHERE token_hash = p_token_hash
     FOR UPDATE;
    IF NOT FOUND OR v_token.purpose IS DISTINCT FROM 'invite' THEN
        token_status := 'missing';
        user_id := '00000000-0000-0000-0000-000000000000';
        tenant_id := '00000000-0000-0000-0000-000000000000';
        membership_id := '00000000-0000-0000-0000-000000000000';
        RETURN NEXT;
        RETURN;
    END IF;

    user_id := v_token.user_id;
    tenant_id := v_token.tenant_id;
    membership_id := v_token.membership_id;

    IF v_token.used_at IS NOT NULL THEN
        token_status := 'used';
        RETURN NEXT;
        RETURN;
    END IF;
    IF v_token.expires_at <= p_now THEN
        token_status := 'expired';
        RETURN NEXT;
        RETURN;
    END IF;

    SELECT u.status INTO v_user_status FROM public.users AS u WHERE u.id = v_token.user_id;
    SELECT m.status INTO v_member_status
      FROM public.memberships AS m
     WHERE m.tenant_id = v_token.tenant_id AND m.id = v_token.membership_id AND m.user_id = v_token.user_id;
    SELECT t.status INTO v_tenant_status FROM public.tenants AS t WHERE t.id = v_token.tenant_id;
    IF v_user_status IS NULL
        OR v_user_status = 'disabled'
        OR v_member_status IS NULL
        OR v_member_status = 'disabled'
        OR v_tenant_status IS DISTINCT FROM 'active'
    THEN
        token_status := 'blocked';
        RETURN NEXT;
        RETURN;
    END IF;

    PERFORM public.auth_set_password_hash(v_token.user_id, p_password_hash);
    UPDATE public.users AS u
       SET status = 'active'
     WHERE u.id = v_token.user_id
       AND u.status IN ('invited', 'active');
    UPDATE public.memberships AS m
       SET status = 'active'
     WHERE m.tenant_id = v_token.tenant_id
       AND m.id = v_token.membership_id
       AND m.user_id = v_token.user_id
       AND m.status IN ('invited', 'active');
    IF NOT FOUND THEN
        RAISE EXCEPTION 'usuário não encontrado'
            USING ERRCODE = 'P0002';
    END IF;
    UPDATE public.sessions AS s
       SET revoked_at = p_now
     WHERE s.user_id = v_token.user_id
       AND s.revoked_at IS NULL;
    UPDATE public.auth_tokens
       SET used_at = p_now
     WHERE id = v_token.id
       AND used_at IS NULL;
    token_status := 'ok';
    RETURN NEXT;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_confirm_password_reset(p_token_hash bytea, p_password_hash text, p_now timestamptz)
RETURNS TABLE (
    token_status text,
    user_id uuid
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_token public.auth_tokens%ROWTYPE;
    v_user_status text;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_token_hash IS NULL
        OR pg_catalog.octet_length(p_token_hash) <> 32
        OR p_password_hash IS NULL
        OR pg_catalog.btrim(p_password_hash) = ''
        OR pg_catalog.length(p_password_hash) > 512
        OR p_password_hash <> pg_catalog.btrim(p_password_hash)
        OR p_now IS NULL
    THEN
        RAISE EXCEPTION 'token inválido'
            USING ERRCODE = '22023';
    END IF;

    SELECT * INTO v_token
      FROM public.auth_tokens
     WHERE token_hash = p_token_hash
     FOR UPDATE;
    IF NOT FOUND OR v_token.purpose IS DISTINCT FROM 'password_reset' THEN
        token_status := 'missing';
        user_id := '00000000-0000-0000-0000-000000000000';
        RETURN NEXT;
        RETURN;
    END IF;

    user_id := v_token.user_id;
    IF v_token.used_at IS NOT NULL THEN
        token_status := 'used';
        RETURN NEXT;
        RETURN;
    END IF;
    IF v_token.expires_at <= p_now THEN
        token_status := 'expired';
        RETURN NEXT;
        RETURN;
    END IF;

    SELECT u.status INTO v_user_status FROM public.users AS u WHERE u.id = v_token.user_id;
    IF v_user_status IS DISTINCT FROM 'active' THEN
        token_status := 'blocked';
        RETURN NEXT;
        RETURN;
    END IF;

    PERFORM public.auth_set_password_hash(v_token.user_id, p_password_hash);
    UPDATE public.sessions AS s
       SET revoked_at = p_now
     WHERE s.user_id = v_token.user_id
       AND s.revoked_at IS NULL;
    UPDATE public.auth_tokens
       SET used_at = p_now
     WHERE id = v_token.id
       AND used_at IS NULL;
    token_status := 'ok';
    RETURN NEXT;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_locked_until(p_kind text, p_subject_hash bytea, p_now timestamptz)
RETURNS timestamptz
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_until timestamptz;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_kind IS NULL
        OR p_kind NOT IN ('login_account', 'login_ip', 'reset_account', 'reset_ip', 'token_ip')
        OR p_subject_hash IS NULL
        OR pg_catalog.octet_length(p_subject_hash) <> 32
        OR p_now IS NULL
    THEN
        RAISE EXCEPTION 'contador inválido'
            USING ERRCODE = '22023';
    END IF;
    SELECT c.locked_until
      INTO v_until
      FROM public.auth_attempt_counters AS c
     WHERE c.kind = p_kind
       AND c.subject_hash = p_subject_hash
       AND c.locked_until IS NOT NULL
       AND c.locked_until > p_now;
    RETURN v_until;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_bump_failure(
    p_kind text,
    p_subject_hash bytea,
    p_now timestamptz,
    p_window interval
)
RETURNS TABLE (
    failures integer,
    locked_until timestamptz,
    already_locked boolean
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_failures integer;
    v_locked timestamptz;
    v_already boolean;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_kind IS NULL
        OR p_kind NOT IN ('login_account', 'login_ip', 'reset_account', 'reset_ip', 'token_ip')
        OR p_subject_hash IS NULL
        OR pg_catalog.octet_length(p_subject_hash) <> 32
        OR p_now IS NULL
        OR p_window IS NULL
    THEN
        RAISE EXCEPTION 'contador inválido'
            USING ERRCODE = '22023';
    END IF;

    -- One statement inserts or locks the existing row. A prior SELECT FOR UPDATE
    -- does not lock a missing key, so two first attempts could both insert and
    -- the loser would roll back without counting.
    WITH bumped AS (
        INSERT INTO public.auth_attempt_counters AS c (kind, subject_hash, failures, window_started_at)
        VALUES (p_kind, p_subject_hash, 1, p_now)
        ON CONFLICT (kind, subject_hash) DO UPDATE
            SET failures = CASE
                    WHEN c.locked_until IS NOT NULL AND c.locked_until > p_now THEN c.failures
                    WHEN p_now - c.window_started_at > p_window THEN 1
                    ELSE c.failures + 1
                END,
                window_started_at = CASE
                    WHEN c.locked_until IS NOT NULL AND c.locked_until > p_now THEN c.window_started_at
                    WHEN p_now - c.window_started_at > p_window THEN p_now
                    ELSE c.window_started_at
                END,
                locked_until = CASE
                    WHEN c.locked_until IS NOT NULL AND c.locked_until > p_now THEN c.locked_until
                    ELSE NULL
                END
        RETURNING c.failures AS bumped_failures,
                  c.locked_until AS bumped_locked_until,
                  (c.locked_until IS NOT NULL AND c.locked_until > p_now) AS bumped_already_locked
    )
    SELECT b.bumped_failures, b.bumped_locked_until, b.bumped_already_locked
      INTO v_failures, v_locked, v_already
      FROM bumped AS b;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'contador inválido'
            USING ERRCODE = '22023';
    END IF;

    failures := v_failures;
    locked_until := v_locked;
    already_locked := v_already;
    RETURN NEXT;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_set_lock(p_kind text, p_subject_hash bytea, p_until timestamptz)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_kind IS NULL
        OR p_subject_hash IS NULL
        OR pg_catalog.octet_length(p_subject_hash) <> 32
        OR p_until IS NULL
    THEN
        RAISE EXCEPTION 'contador inválido'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.auth_attempt_counters
       SET locked_until = p_until
     WHERE kind = p_kind
       AND subject_hash = p_subject_hash;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'contador inválido'
            USING ERRCODE = '22023';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_clear_failures(p_kind text, p_subject_hash bytea)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_kind IS NULL OR p_subject_hash IS NULL OR pg_catalog.octet_length(p_subject_hash) <> 32 THEN
        RAISE EXCEPTION 'contador inválido'
            USING ERRCODE = '22023';
    END IF;
    DELETE FROM public.auth_attempt_counters
     WHERE kind = p_kind
       AND subject_hash = p_subject_hash;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION auth_require_scope() FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_login_identity(text) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_insert_session(uuid, bytea, uuid, uuid, uuid, bytea, timestamptz, timestamptz, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_load_session(bytea, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_touch_session(uuid, timestamptz, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_revoke_by_token_hash(bytea, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_revoke_user_sessions(uuid, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_revoke_session(uuid, uuid, uuid, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_list_tenant_sessions(uuid, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_insert_auth_token(uuid, bytea, text, uuid, uuid, uuid, timestamptz, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_invalidate_auth_token(bytea, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_accept_invite(bytea, text, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_confirm_password_reset(bytea, text, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_locked_until(text, bytea, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_bump_failure(text, bytea, timestamptz, interval) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_set_lock(text, bytea, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_clear_failures(text, bytea) FROM PUBLIC, chatisp_platform, chatisp_ingress;

GRANT EXECUTE ON FUNCTION auth_login_identity(text) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_insert_session(uuid, bytea, uuid, uuid, uuid, bytea, timestamptz, timestamptz, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_load_session(bytea, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_touch_session(uuid, timestamptz, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_revoke_by_token_hash(bytea, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_revoke_user_sessions(uuid, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_revoke_session(uuid, uuid, uuid, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_list_tenant_sessions(uuid, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_insert_auth_token(uuid, bytea, text, uuid, uuid, uuid, timestamptz, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_invalidate_auth_token(bytea, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_accept_invite(bytea, text, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_confirm_password_reset(bytea, text, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_locked_until(text, bytea, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_bump_failure(text, bytea, timestamptz, interval) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_set_lock(text, bytea, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_clear_failures(text, bytea) TO chatisp_app;

-- +goose Down
DROP FUNCTION IF EXISTS auth_clear_failures(text, bytea);
DROP FUNCTION IF EXISTS auth_set_lock(text, bytea, timestamptz);
DROP FUNCTION IF EXISTS auth_bump_failure(text, bytea, timestamptz, interval);
DROP FUNCTION IF EXISTS auth_locked_until(text, bytea, timestamptz);
DROP FUNCTION IF EXISTS auth_confirm_password_reset(bytea, text, timestamptz);
DROP FUNCTION IF EXISTS auth_accept_invite(bytea, text, timestamptz);
DROP FUNCTION IF EXISTS auth_invalidate_auth_token(bytea, timestamptz);
DROP FUNCTION IF EXISTS auth_insert_auth_token(uuid, bytea, text, uuid, uuid, uuid, timestamptz, timestamptz);
DROP FUNCTION IF EXISTS auth_list_tenant_sessions(uuid, timestamptz);
DROP FUNCTION IF EXISTS auth_revoke_session(uuid, uuid, uuid, timestamptz);
DROP FUNCTION IF EXISTS auth_revoke_user_sessions(uuid, timestamptz);
DROP FUNCTION IF EXISTS auth_revoke_by_token_hash(bytea, timestamptz);
DROP FUNCTION IF EXISTS auth_touch_session(uuid, timestamptz, timestamptz);
DROP FUNCTION IF EXISTS auth_load_session(bytea, timestamptz);
DROP FUNCTION IF EXISTS auth_insert_session(uuid, bytea, uuid, uuid, uuid, bytea, timestamptz, timestamptz, timestamptz);
DROP FUNCTION IF EXISTS auth_login_identity(text);
DROP FUNCTION IF EXISTS auth_require_scope();
DROP POLICY IF EXISTS memberships_owner_auth ON memberships;
DROP POLICY IF EXISTS tenants_owner_auth ON tenants;
DROP TABLE IF EXISTS auth_attempt_counters;
DROP TABLE IF EXISTS auth_tokens;
DROP TABLE IF EXISTS sessions;
