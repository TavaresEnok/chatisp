-- +goose Up
-- Synchronize writes made by the old service after migration 00007. Lock the
-- sources until the binding constraint is installed; never overwrite a registry.
LOCK TABLE meta_apps, meta_app_secrets, meta_app_registry, meta_app_registry_secrets IN ACCESS EXCLUSIVE MODE;
ALTER TABLE meta_apps NO FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_secrets NO FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry NO FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry_secrets NO FORCE ROW LEVEL SECURITY;
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (
        SELECT 1 FROM meta_apps a JOIN meta_app_registry r ON r.id = a.id OR r.app_id = a.app_id
        WHERE (r.id, r.app_id, r.status, r.created_at) IS DISTINCT FROM (a.id, a.app_id, a.status, a.created_at)
    ) OR EXISTS (
        SELECT 1 FROM meta_app_secrets l JOIN meta_app_registry_secrets s ON s.registry_id = l.meta_app_id
        WHERE (s.key_version, s.ciphertext, s.aad_scope, s.aad_tenant_id, s.aad_entity, s.aad_entity_id, s.aad_field, s.aad)
          IS DISTINCT FROM (l.key_version, l.ciphertext, 'legacy_tenant', l.tenant_id, 'meta_app', l.meta_app_id, 'app_secret', l.aad)
    ) THEN
        RAISE EXCEPTION 'legacy app conflicts with canonical registry';
    END IF;
END $$;
-- +goose StatementEnd
INSERT INTO meta_app_registry (id, app_id, status, created_at)
SELECT a.id, a.app_id, a.status, a.created_at FROM meta_apps a
WHERE NOT EXISTS (SELECT 1 FROM meta_app_registry r WHERE r.id = a.id);
INSERT INTO meta_app_registry_secrets (registry_id, key_version, ciphertext, aad_scope, aad_tenant_id, aad_entity, aad_entity_id, aad_field, aad)
SELECT l.meta_app_id, l.key_version, l.ciphertext, 'legacy_tenant', l.tenant_id, 'meta_app', l.meta_app_id, 'app_secret', l.aad
FROM meta_app_secrets l
WHERE NOT EXISTS (SELECT 1 FROM meta_app_registry_secrets s WHERE s.registry_id = l.meta_app_id);
ALTER TABLE meta_apps FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_secrets FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry_secrets FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry_secrets ADD COLUMN binding_capability uuid NOT NULL DEFAULT gen_random_uuid();
REVOKE INSERT, UPDATE ON meta_apps FROM chatisp_app;
ALTER TABLE meta_apps DROP CONSTRAINT meta_apps_app_id_key;
ALTER TABLE meta_apps ADD CONSTRAINT meta_apps_tenant_app_id_key UNIQUE (tenant_id, app_id);
ALTER TABLE meta_app_registry ADD CONSTRAINT meta_app_registry_id_app_id_key UNIQUE (id, app_id);
ALTER TABLE meta_apps ADD COLUMN registry_id uuid;
ALTER TABLE meta_apps NO FORCE ROW LEVEL SECURITY;
UPDATE meta_apps SET registry_id = id;
ALTER TABLE meta_apps FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_apps ALTER COLUMN registry_id SET NOT NULL;
ALTER TABLE meta_apps ADD CONSTRAINT meta_apps_registry_fkey FOREIGN KEY (registry_id, app_id) REFERENCES meta_app_registry (id, app_id);

-- The migration owner may access only the two registry tables through this
-- function. FORCE RLS remains enabled and the runtime does not use the owner.
CREATE POLICY meta_apps_owner_binding ON meta_apps TO CURRENT_USER USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid) WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY meta_app_registry_owner_binding ON meta_app_registry TO CURRENT_USER USING (true) WITH CHECK (true);
CREATE POLICY meta_app_registry_secrets_owner_binding ON meta_app_registry_secrets TO CURRENT_USER USING (true) WITH CHECK (true);

-- +goose StatementBegin
CREATE FUNCTION bind_meta_app(p_app_id text, p_binding_id uuid, p_registry_id uuid,
    p_key_version text, p_ciphertext bytea, p_aad_scope text, p_aad_tenant_id uuid,
    p_aad_entity text, p_aad_entity_id uuid, p_aad_field text, p_aad text,
    p_create boolean, p_capability uuid) RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
DECLARE tenant uuid; existing public.meta_app_registry%ROWTYPE; secret public.meta_app_registry_secrets%ROWTYPE; binding uuid;
BEGIN
    tenant := NULLIF(current_setting('app.tenant_id', true), '')::uuid;
    IF tenant IS NULL THEN
        RAISE EXCEPTION 'tenant context required' USING ERRCODE = '42501';
    END IF;
    SELECT * INTO existing FROM public.meta_app_registry WHERE app_id = p_app_id FOR UPDATE;
    IF NOT FOUND THEN
        IF NOT p_create OR p_aad_scope <> 'ingress_registry' OR p_aad_tenant_id IS NOT NULL THEN
            RAISE EXCEPTION 'registry changed' USING ERRCODE = '23505';
        END IF;
        INSERT INTO public.meta_app_registry(id, app_id, status) VALUES (p_registry_id, p_app_id, 'active');
        INSERT INTO public.meta_app_registry_secrets(registry_id, key_version, ciphertext, aad_scope, aad_tenant_id, aad_entity, aad_entity_id, aad_field, aad)
        VALUES (p_registry_id, p_key_version, p_ciphertext, p_aad_scope, p_aad_tenant_id, p_aad_entity, p_aad_entity_id, p_aad_field, p_aad);
    ELSE
        IF p_create OR existing.id <> p_registry_id OR existing.status <> 'active' THEN
            RAISE EXCEPTION 'registry changed' USING ERRCODE = '23505';
        END IF;
        SELECT * INTO secret FROM public.meta_app_registry_secrets WHERE registry_id = existing.id FOR UPDATE;
        IF NOT FOUND OR secret.binding_capability IS DISTINCT FROM p_capability OR (secret.key_version, secret.ciphertext, secret.aad_scope, secret.aad_tenant_id, secret.aad_entity, secret.aad_entity_id, secret.aad_field, secret.aad)
           IS DISTINCT FROM (p_key_version, p_ciphertext, p_aad_scope, p_aad_tenant_id, p_aad_entity, p_aad_entity_id, p_aad_field, p_aad) THEN
            RAISE EXCEPTION 'registry changed' USING ERRCODE = '23505';
        END IF;
    END IF;
    -- Owner is subject to FORCE RLS on this tenant table too.
    INSERT INTO public.meta_apps(tenant_id, id, app_id, status, registry_id)
    VALUES (tenant, p_binding_id, p_app_id, 'active', p_registry_id)
    ON CONFLICT (tenant_id, app_id) DO UPDATE SET app_id = EXCLUDED.app_id
    WHERE public.meta_apps.registry_id = EXCLUDED.registry_id AND public.meta_apps.status = 'active'
    RETURNING id INTO binding;
    IF binding IS NULL THEN RAISE EXCEPTION 'binding conflict' USING ERRCODE = '23505'; END IF;
    RETURN binding;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION bind_meta_app(text, uuid, uuid, text, bytea, text, uuid, text, uuid, text, text, boolean, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION bind_meta_app(text, uuid, uuid, text, bytea, text, uuid, text, uuid, text, text, boolean, uuid) TO chatisp_app;

-- Expose only the key version of a binding visible to the current tenant.
-- +goose StatementBegin
CREATE FUNCTION meta_app_secret_version(p_binding_id uuid) RETURNS text
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog, public AS $$
    SELECT s.key_version FROM public.meta_apps a
    JOIN public.meta_app_registry_secrets s ON s.registry_id = a.registry_id
    WHERE a.id = p_binding_id
      AND a.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION meta_app_secret_version(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION meta_app_secret_version(uuid) TO chatisp_app;

-- +goose Down
-- The migration owner must inspect every tenant and the legacy ciphertext.
-- These DDL changes and the guard are in the same migration transaction.
ALTER TABLE meta_apps NO FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_secrets NO FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry NO FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry_secrets NO FORCE ROW LEVEL SECURITY;
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT app_id FROM meta_apps GROUP BY app_id HAVING count(*) > 1) THEN
        RAISE EXCEPTION 'shared app bindings would be lost';
    END IF;
    IF EXISTS (
        SELECT 1 FROM meta_apps a
        LEFT JOIN meta_app_registry r ON r.id = a.registry_id AND r.id = a.id
            AND r.app_id = a.app_id AND r.status = a.status AND r.created_at = a.created_at
        LEFT JOIN meta_app_registry_secrets s ON s.registry_id = r.id
        LEFT JOIN meta_app_secrets l ON l.tenant_id = a.tenant_id AND l.meta_app_id = a.id
            AND l.key_version = s.key_version AND l.ciphertext = s.ciphertext AND l.aad = s.aad
        WHERE r.id IS NULL OR s.registry_id IS NULL OR l.meta_app_id IS NULL
            OR s.aad_scope <> 'legacy_tenant' OR s.aad_tenant_id IS DISTINCT FROM a.tenant_id
            OR s.aad_entity <> 'meta_app' OR s.aad_entity_id <> a.id OR s.aad_field <> 'app_secret'
            OR s.aad <> a.tenant_id::text || chr(31) || 'meta_app' || chr(31) || a.id::text || chr(31) || 'app_secret'
    ) THEN
        RAISE EXCEPTION 'app binding lacks compatible legacy representation';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE meta_apps FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_secrets FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry_secrets FORCE ROW LEVEL SECURITY;
DROP FUNCTION meta_app_secret_version(uuid);
DROP FUNCTION bind_meta_app(text, uuid, uuid, text, bytea, text, uuid, text, uuid, text, text, boolean, uuid);
GRANT INSERT, UPDATE ON meta_apps TO chatisp_app;
ALTER TABLE meta_app_registry_secrets DROP COLUMN binding_capability;
DROP POLICY meta_apps_owner_binding ON meta_apps;
DROP POLICY meta_app_registry_owner_binding ON meta_app_registry;
DROP POLICY meta_app_registry_secrets_owner_binding ON meta_app_registry_secrets;
ALTER TABLE meta_apps DROP CONSTRAINT meta_apps_registry_fkey;
ALTER TABLE meta_apps DROP COLUMN registry_id;
ALTER TABLE meta_app_registry DROP CONSTRAINT meta_app_registry_id_app_id_key;
ALTER TABLE meta_apps DROP CONSTRAINT meta_apps_tenant_app_id_key;
ALTER TABLE meta_apps ADD CONSTRAINT meta_apps_app_id_key UNIQUE (app_id);
