-- +goose Up
CREATE TABLE collision_indicators (
 tenant_id uuid NOT NULL, conversation_id uuid NOT NULL, membership_id uuid NOT NULL,
 session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE, tab_id uuid NOT NULL,
 typing boolean NOT NULL, expires_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id, conversation_id, membership_id, session_id, tab_id),
 FOREIGN KEY (tenant_id, conversation_id) REFERENCES conversations(tenant_id, id) ON DELETE CASCADE,
 FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships(tenant_id, id) ON DELETE CASCADE
);
CREATE INDEX collision_expiry_idx ON collision_indicators (tenant_id, expires_at);
ALTER TABLE collision_indicators ENABLE ROW LEVEL SECURITY;
ALTER TABLE collision_indicators FORCE ROW LEVEL SECURITY;
CREATE POLICY collision_tenant ON collision_indicators TO chatisp_app
 USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
 WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON collision_indicators TO chatisp_app;

-- Session secrets and global user data never leave this narrowly scoped gate.
-- Lock users before sessions, matching password and MFA reset transactions.
-- +goose StatementBegin
CREATE FUNCTION collision_live_session(p_session uuid, p_tenant uuid, p_member uuid, p_now timestamptz)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE v_user uuid;
BEGIN
 IF p_tenant IS DISTINCT FROM nullif(current_setting('app.tenant_id', true), '')::uuid THEN
   RETURN false;
 END IF;
 SELECT s.user_id INTO v_user FROM public.sessions s
 WHERE s.id=p_session AND s.tenant_id=p_tenant AND s.membership_id=p_member;
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM 1 FROM public.users u WHERE u.id=v_user AND u.status='active' FOR SHARE;
 IF NOT FOUND THEN RETURN false; END IF;
 PERFORM 1 FROM public.sessions s
 WHERE s.id=p_session AND s.user_id=v_user AND s.tenant_id=p_tenant AND s.membership_id=p_member
   AND s.revoked_at IS NULL AND s.idle_expires_at>p_now AND s.absolute_expires_at>p_now FOR SHARE;
 RETURN FOUND;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION collision_live_session(uuid, uuid, uuid, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION collision_live_session(uuid, uuid, uuid, timestamptz) TO chatisp_app;

-- +goose Down
DROP FUNCTION collision_live_session(uuid, uuid, uuid, timestamptz);
DROP TABLE collision_indicators;
