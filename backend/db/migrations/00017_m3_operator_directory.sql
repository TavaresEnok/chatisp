-- +goose Up
-- Operational directory: minimal identity projection (membership id, role and
-- the e-mail used as label, since users has no display name) for active members
-- who can operate one inbox. The caller's tenant, membership, user, role and
-- inbox scope are re-read from persisted rows; arguments never grant access.
-- chatisp_app gets EXECUTE only; it still has no SELECT on users.
--
-- The owner is subject to FORCE RLS, so the definer needs SELECT-only owner
-- policies. They are bound to the caller's own tenant and to a flag that only
-- operator_directory raises (and lowers) inside its transaction; the app role
-- already sees exactly these rows through its own policies.
-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
 FOREACH tab IN ARRAY ARRAY['memberships','sectors','sector_memberships','sector_inboxes','inboxes'] LOOP
  EXECUTE format('CREATE POLICY directory_owner_read ON %I FOR SELECT TO chatisp_owner USING (tenant_id = NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid AND current_setting(''app.directory_read'',true) = ''on'')', tab);
 END LOOP;
END $$;
-- +goose StatementEnd
CREATE POLICY directory_owner_read ON tenants FOR SELECT TO chatisp_owner
 USING (id = NULLIF(current_setting('app.tenant_id',true),'')::uuid AND current_setting('app.directory_read',true) = 'on');

-- +goose StatementBegin
CREATE FUNCTION operator_directory(p_tenant uuid, p_user uuid, p_member uuid, p_inbox uuid, p_after uuid, p_limit integer)
RETURNS TABLE (membership_id uuid, role text, label text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE v_role text; v_ok boolean := false;
BEGIN
 IF p_tenant IS NULL OR p_tenant IS DISTINCT FROM nullif(current_setting('app.tenant_id', true), '')::uuid
    OR p_limit IS NULL OR p_limit < 1 OR p_limit > 200 THEN
   RETURN;
 END IF;
 PERFORM set_config('app.directory_read', 'on', true);
 PERFORM 1 FROM public.users u WHERE u.id=p_user AND u.status='active';
 IF FOUND THEN
  SELECT m.role INTO v_role FROM public.memberships m
   WHERE m.tenant_id=p_tenant AND m.id=p_member AND m.user_id=p_user AND m.status='active'
     AND m.role IN ('admin','supervisor','agent');
  IF FOUND AND EXISTS (SELECT 1 FROM public.tenants t WHERE t.id=p_tenant AND t.status='active')
     AND EXISTS (SELECT 1 FROM public.inboxes i WHERE i.tenant_id=p_tenant AND i.id=p_inbox) THEN
   v_ok := v_role <> 'agent' OR EXISTS (
     SELECT 1 FROM public.sector_memberships sm
     JOIN public.sectors s ON s.tenant_id=sm.tenant_id AND s.id=sm.sector_id AND s.active
     JOIN public.sector_inboxes si ON si.tenant_id=sm.tenant_id AND si.sector_id=sm.sector_id AND si.inbox_id=p_inbox
     WHERE sm.tenant_id=p_tenant AND sm.membership_id=p_member);
  END IF;
 END IF;
 IF v_ok THEN
  RETURN QUERY
   SELECT m.id, m.role, u.email
   FROM public.memberships m JOIN public.users u ON u.id=m.user_id AND u.status='active'
   WHERE m.tenant_id=p_tenant AND m.status='active' AND m.role IN ('admin','supervisor','agent')
     AND (p_after IS NULL OR m.id > p_after)
     AND (m.role IN ('admin','supervisor') OR EXISTS (
       SELECT 1 FROM public.sector_memberships sm
       JOIN public.sectors s ON s.tenant_id=sm.tenant_id AND s.id=sm.sector_id AND s.active
       JOIN public.sector_inboxes si ON si.tenant_id=sm.tenant_id AND si.sector_id=sm.sector_id AND si.inbox_id=p_inbox
       WHERE sm.tenant_id=p_tenant AND sm.membership_id=m.id
         AND (v_role <> 'agent' OR EXISTS (
           SELECT 1 FROM public.sector_memberships caller_sm
           WHERE caller_sm.tenant_id=p_tenant AND caller_sm.membership_id=p_member
             AND caller_sm.sector_id=sm.sector_id))))
   ORDER BY m.id LIMIT p_limit;
 END IF;
 PERFORM set_config('app.directory_read', '', true);
 RETURN;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION operator_directory(uuid, uuid, uuid, uuid, uuid, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION operator_directory(uuid, uuid, uuid, uuid, uuid, integer) TO chatisp_app;

-- +goose Down
DROP FUNCTION operator_directory(uuid, uuid, uuid, uuid, uuid, integer);
DROP POLICY directory_owner_read ON tenants;
DROP POLICY directory_owner_read ON inboxes;
DROP POLICY directory_owner_read ON sector_inboxes;
DROP POLICY directory_owner_read ON sector_memberships;
DROP POLICY directory_owner_read ON sectors;
DROP POLICY directory_owner_read ON memberships;
