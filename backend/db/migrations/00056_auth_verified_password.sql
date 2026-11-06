-- +goose Up
-- Keep the verified credential stable until the session insertion commits.
-- Password reset updates the same user row before revoking sessions.
-- +goose StatementBegin
CREATE FUNCTION auth_verified_password_current(p_user_id uuid, p_verified_hash text, p_mfa_ciphertext bytea, p_mfa_confirmed boolean, p_superadmin boolean)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    v_hash text;
    v_ciphertext bytea;
    v_confirmed boolean;
    v_superadmin boolean;
BEGIN
    PERFORM public.auth_require_scope();
    IF p_user_id IS NULL OR p_verified_hash IS NULL OR p_verified_hash = '' THEN
        RETURN false;
    END IF;
    SELECT password_hash, totp_ciphertext, totp_confirmed_at IS NOT NULL, is_superadmin
      INTO v_hash, v_ciphertext, v_confirmed, v_superadmin FROM public.users WHERE id = p_user_id FOR SHARE;
    RETURN FOUND AND v_hash IS NOT NULL AND v_hash = p_verified_hash
      AND v_ciphertext IS NOT DISTINCT FROM p_mfa_ciphertext
      AND v_confirmed IS NOT DISTINCT FROM p_mfa_confirmed
      AND v_superadmin IS NOT DISTINCT FROM p_superadmin;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION auth_verified_password_current(uuid, text, bytea, boolean, boolean) FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION auth_verified_password_current(uuid, text, bytea, boolean, boolean) TO chatisp_app;

-- +goose StatementBegin
CREATE FUNCTION platform_revoke_tenant_sessions(p_tenant_id uuid, p_now timestamptz)
RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    IF current_setting('app.access_scope', true) IS DISTINCT FROM 'platform' THEN
        RAISE EXCEPTION 'contexto de plataforma ausente' USING ERRCODE = '42501';
    END IF;
    IF p_tenant_id IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION 'sessão inválida' USING ERRCODE = '22023';
    END IF;
    UPDATE public.sessions SET revoked_at = p_now WHERE tenant_id = p_tenant_id AND revoked_at IS NULL;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform_revoke_tenant_sessions(uuid, timestamptz) FROM PUBLIC, chatisp_app, chatisp_ingress;
GRANT EXECUTE ON FUNCTION platform_revoke_tenant_sessions(uuid, timestamptz) TO chatisp_platform;

-- +goose Down
DROP FUNCTION IF EXISTS platform_revoke_tenant_sessions(uuid, timestamptz);
DROP FUNCTION IF EXISTS auth_verified_password_current(uuid, text, bytea, boolean, boolean);
