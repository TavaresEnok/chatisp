-- +goose Up
-- Presença por sessão e aba. O agregado do atendente fica em presence_operator.
-- Este marco não desatribui conversas nem escreve capacidade, fila ou horário.
CREATE TABLE presence_connections (
    tenant_id uuid NOT NULL,
    session_id uuid NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    tab_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES users (id),
    membership_id uuid NOT NULL,
    heartbeat_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, session_id, tab_id),
    FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships (tenant_id, id),
    CHECK (expires_at > heartbeat_at)
);
CREATE INDEX presence_connections_user_idx ON presence_connections (tenant_id, user_id, expires_at);

CREATE TABLE presence_operator (
    tenant_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES users (id),
    membership_id uuid NOT NULL,
    manual text NOT NULL DEFAULT 'available' CHECK (manual IN ('available', 'busy')),
    explicit_offline_at timestamptz,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (tenant_id, user_id),
    FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships (tenant_id, id)
);

CREATE TABLE presence_settings (
    tenant_id uuid PRIMARY KEY REFERENCES tenants (id),
    offline_tolerance_seconds integer NOT NULL CHECK (offline_tolerance_seconds BETWEEN 0 AND 86400),
    version bigint NOT NULL CHECK (version > 0)
);

CREATE TABLE presence_commands (
    tenant_id uuid NOT NULL,
    membership_id uuid NOT NULL,
    request_id uuid NOT NULL,
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    response jsonb NOT NULL CHECK (jsonb_typeof(response) = 'object'),
    PRIMARY KEY (tenant_id, membership_id, request_id),
    FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships (tenant_id, id)
);

-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
  FOREACH tab IN ARRAY ARRAY['presence_connections','presence_operator','presence_settings','presence_commands'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', tab);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', tab);
    EXECUTE format($p$CREATE POLICY tenant_isolation ON %I TO chatisp_app USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)$p$, tab);
  END LOOP;
END $$;
-- +goose StatementEnd

GRANT SELECT, INSERT, UPDATE, DELETE ON presence_connections TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON presence_operator TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON presence_settings TO chatisp_app;
GRANT SELECT, INSERT ON presence_commands TO chatisp_app;

-- Boolean gate only. Resolves the user from the session row, then locks users
-- before sessions. No column of users or sessions is returned to chatisp_app.
-- +goose StatementBegin
CREATE FUNCTION presence_session_live(p_session uuid, p_tenant uuid, p_member uuid, p_user uuid, p_now timestamptz)
RETURNS boolean
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
DECLARE
    v_user uuid;
BEGIN
    IF p_now IS NULL OR p_session IS NULL OR p_member IS NULL OR p_user IS NULL
       OR p_tenant IS DISTINCT FROM nullif(current_setting('app.tenant_id', true), '')::uuid THEN
        RETURN false;
    END IF;
    SELECT s.user_id INTO v_user
      FROM public.sessions AS s
     WHERE s.id = p_session
       AND s.tenant_id = p_tenant
       AND s.membership_id = p_member;
    IF NOT FOUND OR v_user IS DISTINCT FROM p_user THEN
        RETURN false;
    END IF;
    PERFORM 1 FROM public.users AS u WHERE u.id = v_user AND u.status = 'active' FOR SHARE;
    IF NOT FOUND THEN
        RETURN false;
    END IF;
    PERFORM 1
       FROM public.sessions AS s
      WHERE s.id = p_session
        AND s.user_id = v_user
        AND s.tenant_id = p_tenant
        AND s.membership_id = p_member
        AND s.revoked_at IS NULL
        AND s.idle_expires_at > p_now
        AND s.absolute_expires_at > p_now
        FOR SHARE;
    RETURN FOUND;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION presence_session_live(uuid, uuid, uuid, uuid, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION presence_session_live(uuid, uuid, uuid, uuid, timestamptz) TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback descartaria presença, tolerância e comandos';
END $$;
-- +goose StatementEnd
