-- +goose Up
-- Enrollment and login serialize on the user row, including the verified secret.
-- +goose StatementBegin
CREATE FUNCTION auth_mfa_begin_optional(p_user uuid, p_ciphertext bytea, p_version text, p_tenant uuid, p_session uuid, p_now timestamptz)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_user IS NULL OR p_ciphertext IS NULL OR octet_length(p_ciphertext) NOT BETWEEN 28 AND 1024
       OR p_version IS NULL OR p_version !~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$' OR p_tenant IS NULL OR p_session IS NULL OR p_now IS NULL THEN
        RAISE EXCEPTION 'segredo de MFA inválido' USING ERRCODE = '22023';
    END IF;
    PERFORM 1 FROM public.users WHERE id = p_user AND status = 'active' AND totp_confirmed_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM public.sessions s JOIN public.memberships m ON m.id = s.membership_id AND m.tenant_id = s.tenant_id AND m.user_id = s.user_id
        JOIN public.tenants t ON t.id = s.tenant_id
        WHERE s.id = p_session AND s.user_id = p_user AND s.tenant_id = p_tenant AND m.status = 'active' AND t.status = 'active'
        AND s.revoked_at IS NULL AND s.idle_expires_at > p_now AND s.absolute_expires_at > p_now FOR UPDATE OF s;
    IF NOT FOUND THEN RETURN false; END IF;
    UPDATE public.users SET totp_ciphertext = p_ciphertext, totp_key_version = p_version,
        totp_aad_tenant = p_tenant, totp_confirmed_at = NULL
      WHERE id = p_user AND totp_confirmed_at IS NULL;
    IF NOT FOUND THEN RETURN false; END IF;
    DELETE FROM public.mfa_recovery_codes WHERE user_id = p_user;
    RETURN true;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION auth_mfa_confirm_optional(p_user uuid, p_session uuid, p_ciphertext bytea, p_now timestamptz)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_user IS NULL OR p_session IS NULL OR p_ciphertext IS NULL OR p_now IS NULL THEN RETURN false; END IF;
    PERFORM 1 FROM public.users WHERE id = p_user AND status = 'active' AND totp_confirmed_at IS NULL
        AND totp_ciphertext = p_ciphertext FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM public.sessions s JOIN public.memberships m ON m.id = s.membership_id AND m.tenant_id = s.tenant_id AND m.user_id = s.user_id
        JOIN public.tenants t ON t.id = s.tenant_id
        WHERE s.id = p_session AND s.user_id = p_user AND m.status = 'active' AND t.status = 'active'
        AND s.revoked_at IS NULL AND s.idle_expires_at > p_now AND s.absolute_expires_at > p_now FOR UPDATE OF s;
    IF NOT FOUND THEN RETURN false; END IF;
    UPDATE public.users SET totp_confirmed_at = p_now WHERE id = p_user;
    UPDATE public.sessions SET revoked_at = p_now WHERE user_id = p_user AND id <> p_session AND revoked_at IS NULL;
    UPDATE public.sessions SET assurance = 'full' WHERE id = p_session;
    RETURN true;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION auth_mfa_reset_optional(p_actor uuid, p_session uuid, p_tenant uuid, p_target uuid, p_now timestamptz)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    PERFORM public.auth_require_scope();
    IF p_actor IS NULL OR p_session IS NULL OR p_tenant IS NULL OR p_target IS NULL OR p_now IS NULL THEN RETURN false; END IF;
    PERFORM 1 FROM public.users WHERE id IN (p_actor, p_target) ORDER BY id FOR UPDATE;
    PERFORM 1 FROM public.sessions s JOIN public.users u ON u.id = s.user_id
        JOIN public.memberships m ON m.id = s.membership_id AND m.user_id = s.user_id AND m.tenant_id = s.tenant_id
        JOIN public.tenants t ON t.id = s.tenant_id
        WHERE s.id = p_session AND s.user_id = p_actor AND s.tenant_id = p_tenant
        AND u.status = 'active' AND u.totp_confirmed_at IS NOT NULL AND m.status = 'active'
        AND (m.role = 'admin' OR u.is_superadmin) AND t.status = 'active'
        AND s.assurance = 'full' AND s.revoked_at IS NULL AND s.idle_expires_at > p_now AND s.absolute_expires_at > p_now FOR UPDATE OF s;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM public.memberships WHERE tenant_id = p_tenant AND user_id = p_target AND status <> 'disabled';
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM public.auth_mfa_clear(p_target, p_now);
    RETURN true;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION auth_mfa_reset_optional(uuid, uuid, uuid, uuid, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION auth_mfa_reset_optional(uuid, uuid, uuid, uuid, timestamptz) TO chatisp_app;
REVOKE EXECUTE ON FUNCTION auth_mfa_clear(uuid, timestamptz) FROM chatisp_app;
REVOKE ALL ON FUNCTION auth_mfa_begin_optional(uuid, bytea, text, uuid, uuid, timestamptz), auth_mfa_confirm_optional(uuid, uuid, bytea, timestamptz) FROM PUBLIC, chatisp_platform, chatisp_ingress;
GRANT EXECUTE ON FUNCTION auth_mfa_begin_optional(uuid, bytea, text, uuid, uuid, timestamptz), auth_mfa_confirm_optional(uuid, uuid, bytea, timestamptz) TO chatisp_app;
REVOKE EXECUTE ON FUNCTION auth_mfa_begin(uuid, bytea, text, uuid), auth_mfa_confirm(uuid, timestamptz) FROM chatisp_app;

-- +goose Down
GRANT EXECUTE ON FUNCTION auth_mfa_clear(uuid, timestamptz) TO chatisp_app;
DROP FUNCTION auth_mfa_reset_optional(uuid, uuid, uuid, uuid, timestamptz);
GRANT EXECUTE ON FUNCTION auth_mfa_begin(uuid, bytea, text, uuid), auth_mfa_confirm(uuid, timestamptz) TO chatisp_app;
DROP FUNCTION auth_mfa_confirm_optional(uuid, uuid, bytea, timestamptz);
DROP FUNCTION auth_mfa_begin_optional(uuid, bytea, text, uuid, uuid, timestamptz);
