-- +goose Up
-- MFA material stays on the restricted auth path.
-- The TOTP secret is an AES-GCM ciphertext. Recovery codes are hashes.
-- Master keys are not stored here.

ALTER TABLE users
    ADD COLUMN totp_ciphertext bytea,
    ADD COLUMN totp_key_version text,
    ADD COLUMN totp_aad_tenant uuid,
    ADD COLUMN totp_confirmed_at timestamptz,
    ADD COLUMN is_superadmin boolean NOT NULL DEFAULT false;

ALTER TABLE users
    ADD CONSTRAINT users_totp_shape CHECK (
        (
            totp_ciphertext IS NULL
            AND totp_key_version IS NULL
            AND totp_aad_tenant IS NULL
            AND totp_confirmed_at IS NULL
        )
        OR (
            totp_ciphertext IS NOT NULL
            AND octet_length(totp_ciphertext) BETWEEN 28 AND 1024
            AND totp_key_version IS NOT NULL
            AND btrim(totp_key_version) <> ''
            AND char_length(totp_key_version) <= 32
            AND totp_aad_tenant IS NOT NULL
        )
    );

ALTER TABLE sessions
    ADD COLUMN assurance text NOT NULL DEFAULT 'full'
        CHECK (assurance IN ('full', 'enrollment'));

CREATE TABLE mfa_recovery_codes (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL,
    code_hash bytea NOT NULL,
    created_at timestamptz NOT NULL,
    used_at timestamptz,
    CONSTRAINT mfa_recovery_codes_hash_len CHECK (octet_length(code_hash) = 32),
    CONSTRAINT mfa_recovery_codes_user_fkey FOREIGN KEY (user_id) REFERENCES users (id),
    CONSTRAINT mfa_recovery_codes_user_hash_key UNIQUE (user_id, code_hash)
);

CREATE INDEX mfa_recovery_codes_user_unused_idx
    ON mfa_recovery_codes (user_id)
    WHERE used_at IS NULL;

REVOKE ALL ON TABLE mfa_recovery_codes FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;

-- +goose StatementBegin
DO $$
DECLARE
    v_name text;
BEGIN
    SELECT c.conname INTO v_name
      FROM pg_constraint AS c
      JOIN pg_class AS t ON t.oid = c.conrelid
      JOIN pg_namespace AS n ON n.oid = t.relnamespace
     WHERE n.nspname = 'public'
       AND t.relname = 'auth_attempt_counters'
       AND c.contype = 'c'
       AND pg_catalog.pg_get_constraintdef(c.oid) LIKE '%login_account%';
    IF v_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE public.auth_attempt_counters DROP CONSTRAINT %I', v_name);
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE auth_attempt_counters
    ADD CONSTRAINT auth_attempt_counters_kind_check CHECK (
        kind IN (
            'login_account', 'login_ip', 'reset_account', 'reset_ip', 'token_ip',
            'mfa_account', 'mfa_ip'
        )
    );

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION auth_locked_until(p_kind text, p_subject_hash bytea, p_now timestamptz)
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
        OR p_kind NOT IN (
            'login_account', 'login_ip', 'reset_account', 'reset_ip', 'token_ip',
            'mfa_account', 'mfa_ip'
        )
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
CREATE OR REPLACE FUNCTION auth_bump_failure(
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
        OR p_kind NOT IN (
            'login_account', 'login_ip', 'reset_account', 'reset_ip', 'token_ip',
            'mfa_account', 'mfa_ip'
        )
        OR p_subject_hash IS NULL
        OR pg_catalog.octet_length(p_subject_hash) <> 32
        OR p_now IS NULL
        OR p_window IS NULL
    THEN
        RAISE EXCEPTION 'contador inválido'
            USING ERRCODE = '22023';
    END IF;

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
CREATE FUNCTION auth_session_gate(p_token_hash bytea)
RETURNS TABLE (
    assurance text,
    totp_confirmed boolean,
    is_superadmin boolean
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_token_hash IS NULL OR pg_catalog.octet_length(p_token_hash) <> 32 THEN
        RAISE EXCEPTION 'sessão inválida'
            USING ERRCODE = '22023';
    END IF;
    RETURN QUERY
    SELECT s.assurance,
           (u.totp_confirmed_at IS NOT NULL),
           u.is_superadmin
      FROM public.sessions AS s
      JOIN public.users AS u ON u.id = s.user_id
     WHERE s.token_hash = p_token_hash;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_set_session_assurance(p_id uuid, p_assurance text)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_id IS NULL OR p_assurance IS NULL OR p_assurance NOT IN ('full', 'enrollment') THEN
        RAISE EXCEPTION 'sessão inválida'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.sessions
       SET assurance = p_assurance
     WHERE id = p_id
       AND revoked_at IS NULL;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'sessão inválida'
            USING ERRCODE = 'P0002';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_mfa_record(p_user_id uuid)
RETURNS TABLE (
    totp_ciphertext bytea,
    totp_key_version text,
    totp_aad_tenant uuid,
    totp_confirmed boolean,
    is_superadmin boolean
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_user_id IS NULL THEN
        RAISE EXCEPTION 'usuário não encontrado'
            USING ERRCODE = 'P0002';
    END IF;
    RETURN QUERY
    SELECT u.totp_ciphertext,
           COALESCE(u.totp_key_version, ''),
           u.totp_aad_tenant,
           (u.totp_confirmed_at IS NOT NULL),
           u.is_superadmin
      FROM public.users AS u
     WHERE u.id = p_user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'usuário não encontrado'
            USING ERRCODE = 'P0002';
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_mfa_begin(
    p_user_id uuid,
    p_ciphertext bytea,
    p_version text,
    p_tenant uuid
)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_user_id IS NULL
        OR p_ciphertext IS NULL
        OR pg_catalog.octet_length(p_ciphertext) < 28
        OR pg_catalog.octet_length(p_ciphertext) > 1024
        OR p_version IS NULL
        OR pg_catalog.btrim(p_version) = ''
        OR pg_catalog.char_length(p_version) > 32
        OR p_version !~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$'
        OR p_tenant IS NULL
    THEN
        RAISE EXCEPTION 'segredo de MFA inválido'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.users
       SET totp_ciphertext = p_ciphertext,
           totp_key_version = p_version,
           totp_aad_tenant = p_tenant,
           totp_confirmed_at = NULL
     WHERE id = p_user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'usuário não encontrado'
            USING ERRCODE = 'P0002';
    END IF;
    DELETE FROM public.mfa_recovery_codes WHERE user_id = p_user_id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_mfa_add_code(
    p_id uuid,
    p_user_id uuid,
    p_code_hash bytea,
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
        OR p_code_hash IS NULL
        OR pg_catalog.octet_length(p_code_hash) <> 32
        OR p_now IS NULL
    THEN
        RAISE EXCEPTION 'código de recuperação inválido'
            USING ERRCODE = '22023';
    END IF;
    INSERT INTO public.mfa_recovery_codes (id, user_id, code_hash, created_at)
    VALUES (p_id, p_user_id, p_code_hash, p_now);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_mfa_confirm(p_user_id uuid, p_now timestamptz)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_found boolean;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_user_id IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION 'segredo de MFA inválido'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.users
       SET totp_confirmed_at = COALESCE(totp_confirmed_at, p_now)
     WHERE id = p_user_id
       AND totp_ciphertext IS NOT NULL;
    v_found := FOUND;
    RETURN v_found;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_mfa_clear(p_user_id uuid, p_now timestamptz)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_user_id IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION 'usuário não encontrado'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.users
       SET totp_ciphertext = NULL,
           totp_key_version = NULL,
           totp_aad_tenant = NULL,
           totp_confirmed_at = NULL
     WHERE id = p_user_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'usuário não encontrado'
            USING ERRCODE = 'P0002';
    END IF;
    DELETE FROM public.mfa_recovery_codes WHERE user_id = p_user_id;
    PERFORM public.auth_revoke_user_sessions(p_user_id, p_now);
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_mfa_list_codes(p_user_id uuid)
RETURNS TABLE (
    id uuid,
    code_hash bytea
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_user_id IS NULL THEN
        RAISE EXCEPTION 'código de recuperação inválido'
            USING ERRCODE = '22023';
    END IF;
    RETURN QUERY
    SELECT c.id, c.code_hash
      FROM public.mfa_recovery_codes AS c
     WHERE c.user_id = p_user_id
       AND c.used_at IS NULL
     ORDER BY c.created_at, c.id;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION auth_mfa_consume_code(p_user_id uuid, p_id uuid, p_now timestamptz)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    v_found boolean;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_user_id IS NULL OR p_id IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION 'código de recuperação inválido'
            USING ERRCODE = '22023';
    END IF;
    UPDATE public.mfa_recovery_codes
       SET used_at = p_now
     WHERE id = p_id
       AND user_id = p_user_id
       AND used_at IS NULL;
    v_found := FOUND;
    RETURN v_found;
END;
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION auth_session_gate(bytea) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_set_session_assurance(uuid, text) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_mfa_record(uuid) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_mfa_begin(uuid, bytea, text, uuid) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_mfa_add_code(uuid, uuid, bytea, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_mfa_confirm(uuid, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_mfa_clear(uuid, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_mfa_list_codes(uuid) FROM PUBLIC, chatisp_platform, chatisp_ingress;
REVOKE ALL ON FUNCTION auth_mfa_consume_code(uuid, uuid, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;

GRANT EXECUTE ON FUNCTION auth_session_gate(bytea) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_set_session_assurance(uuid, text) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_mfa_record(uuid) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_mfa_begin(uuid, bytea, text, uuid) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_mfa_add_code(uuid, uuid, bytea, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_mfa_confirm(uuid, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_mfa_clear(uuid, timestamptz) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_mfa_list_codes(uuid) TO chatisp_app;
GRANT EXECUTE ON FUNCTION auth_mfa_consume_code(uuid, uuid, timestamptz) TO chatisp_app;

-- +goose Down
DROP FUNCTION IF EXISTS auth_mfa_consume_code(uuid, uuid, timestamptz);
DROP FUNCTION IF EXISTS auth_mfa_list_codes(uuid);
DROP FUNCTION IF EXISTS auth_mfa_clear(uuid, timestamptz);
DROP FUNCTION IF EXISTS auth_mfa_confirm(uuid, timestamptz);
DROP FUNCTION IF EXISTS auth_mfa_add_code(uuid, uuid, bytea, timestamptz);
DROP FUNCTION IF EXISTS auth_mfa_begin(uuid, bytea, text, uuid);
DROP FUNCTION IF EXISTS auth_mfa_record(uuid);
DROP FUNCTION IF EXISTS auth_set_session_assurance(uuid, text);
DROP FUNCTION IF EXISTS auth_session_gate(bytea);

DROP TABLE IF EXISTS mfa_recovery_codes;

ALTER TABLE sessions DROP COLUMN IF EXISTS assurance;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_totp_shape;
ALTER TABLE users
    DROP COLUMN IF EXISTS totp_ciphertext,
    DROP COLUMN IF EXISTS totp_key_version,
    DROP COLUMN IF EXISTS totp_aad_tenant,
    DROP COLUMN IF EXISTS totp_confirmed_at,
    DROP COLUMN IF EXISTS is_superadmin;

ALTER TABLE auth_attempt_counters DROP CONSTRAINT IF EXISTS auth_attempt_counters_kind_check;
ALTER TABLE auth_attempt_counters
    ADD CONSTRAINT auth_attempt_counters_kind_check CHECK (
        kind IN ('login_account', 'login_ip', 'reset_account', 'reset_ip', 'token_ip')
    );
