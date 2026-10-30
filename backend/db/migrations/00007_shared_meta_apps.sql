-- +goose Up
-- A migration runs as the owner; FORCE RLS otherwise hides legacy rows.
-- Both changes are transactional and the original policies remain intact.
ALTER TABLE meta_apps NO FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_secrets NO FORCE ROW LEVEL SECURITY;

CREATE TABLE meta_app_registry (
    id uuid PRIMARY KEY,
    app_id text NOT NULL UNIQUE,
    status text NOT NULL CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT meta_app_registry_app_id_chk CHECK (char_length(app_id) BETWEEN 1 AND 64 AND app_id = btrim(app_id))
);

CREATE TABLE meta_app_registry_secrets (
    registry_id uuid PRIMARY KEY REFERENCES meta_app_registry (id),
    key_version text NOT NULL CHECK (char_length(key_version) BETWEEN 1 AND 64),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 1 AND 8192),
    aad_scope text NOT NULL,
    aad_tenant_id uuid,
    aad_entity text NOT NULL,
    aad_entity_id uuid NOT NULL,
    aad_field text NOT NULL,
    aad text NOT NULL,
    CONSTRAINT meta_app_registry_secrets_context_chk CHECK (
        (aad_scope = 'legacy_tenant' AND aad_tenant_id IS NOT NULL
         AND aad_entity = 'meta_app' AND aad_entity_id = registry_id AND aad_field = 'app_secret'
         AND aad = aad_tenant_id::text || chr(31) || aad_entity || chr(31) || aad_entity_id::text || chr(31) || aad_field)
        OR
        (aad_scope = 'ingress_registry' AND aad_tenant_id IS NULL
         AND aad_entity = 'meta_app_registry' AND aad_entity_id = registry_id AND aad_field = 'app_secret'
         AND aad = 'ingress_registry' || chr(31) || aad_entity || chr(31) || aad_entity_id::text || chr(31) || aad_field)
    )
);


-- Reject malformed rows before PostgreSQL can include their ciphertext in a
-- constraint-violation DETAIL. Constraints remain the authoritative invariant.
-- +goose StatementBegin
CREATE FUNCTION meta_app_registry_secret_guard() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF (NEW.registry_id IS NOT NULL AND NEW.key_version IS NOT NULL
        AND char_length(NEW.key_version) BETWEEN 1 AND 64
        AND NEW.ciphertext IS NOT NULL AND octet_length(NEW.ciphertext) BETWEEN 1 AND 8192
        AND NEW.aad_scope IS NOT NULL AND NEW.aad_entity IS NOT NULL
        AND NEW.aad_entity_id IS NOT NULL AND NEW.aad_field IS NOT NULL AND NEW.aad IS NOT NULL
        AND ((NEW.aad_scope = 'legacy_tenant' AND NEW.aad_tenant_id IS NOT NULL
              AND NEW.aad_entity = 'meta_app' AND NEW.aad_entity_id = NEW.registry_id
              AND NEW.aad_field = 'app_secret'
              AND NEW.aad = NEW.aad_tenant_id::text || chr(31) || NEW.aad_entity || chr(31) || NEW.aad_entity_id::text || chr(31) || NEW.aad_field)
          OR (NEW.aad_scope = 'ingress_registry' AND NEW.aad_tenant_id IS NULL
              AND NEW.aad_entity = 'meta_app_registry' AND NEW.aad_entity_id = NEW.registry_id
              AND NEW.aad_field = 'app_secret'
              AND NEW.aad = 'ingress_registry' || chr(31) || NEW.aad_entity || chr(31) || NEW.aad_entity_id::text || chr(31) || NEW.aad_field))) IS NOT TRUE THEN
        RAISE EXCEPTION 'contexto ou segredo do registro inválido' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER meta_app_registry_secret_guard BEFORE INSERT OR UPDATE ON meta_app_registry_secrets
    FOR EACH ROW EXECUTE FUNCTION meta_app_registry_secret_guard();
REVOKE ALL ON FUNCTION meta_app_registry_secret_guard() FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;

INSERT INTO meta_app_registry (id, app_id, status, created_at)
SELECT id, app_id, status, created_at FROM meta_apps;
INSERT INTO meta_app_registry_secrets (registry_id, key_version, ciphertext, aad_scope, aad_tenant_id, aad_entity, aad_entity_id, aad_field, aad)
SELECT meta_app_id, key_version, ciphertext, 'legacy_tenant', tenant_id, 'meta_app', meta_app_id, 'app_secret', aad
FROM meta_app_secrets;
ALTER TABLE meta_apps FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_secrets FORCE ROW LEVEL SECURITY;

ALTER TABLE meta_app_registry ENABLE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry_secrets ENABLE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry_secrets FORCE ROW LEVEL SECURITY;
CREATE POLICY meta_app_registry_ingress ON meta_app_registry FOR ALL TO chatisp_ingress
    USING (current_setting('app.access_scope', true) = 'ingress')
    WITH CHECK (current_setting('app.access_scope', true) = 'ingress');
CREATE POLICY meta_app_registry_secrets_ingress ON meta_app_registry_secrets FOR ALL TO chatisp_ingress
    USING (current_setting('app.access_scope', true) = 'ingress')
    WITH CHECK (current_setting('app.access_scope', true) = 'ingress');
REVOKE ALL ON meta_app_registry, meta_app_registry_secrets FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT, UPDATE, DELETE ON meta_app_registry, meta_app_registry_secrets TO chatisp_ingress;

-- +goose Down
-- The owner needs a complete view to detect any global data that would be lost.
ALTER TABLE meta_app_registry NO FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_registry_secrets NO FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_apps NO FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_secrets NO FORCE ROW LEVEL SECURITY;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM meta_app_registry r
        LEFT JOIN meta_apps a ON a.id = r.id AND a.app_id = r.app_id
            AND a.status = r.status AND a.created_at = r.created_at
        WHERE a.id IS NULL
    ) OR EXISTS (
        SELECT 1 FROM meta_app_registry_secrets s
        LEFT JOIN meta_app_secrets l ON l.tenant_id = s.aad_tenant_id AND l.meta_app_id = s.registry_id
            AND l.key_version = s.key_version AND l.ciphertext = s.ciphertext AND l.aad = s.aad
        WHERE s.aad_scope <> 'legacy_tenant' OR l.meta_app_id IS NULL
    ) THEN
        RAISE EXCEPTION 'registro global possui dados que seriam perdidos na reversão';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE meta_apps FORCE ROW LEVEL SECURITY;
ALTER TABLE meta_app_secrets FORCE ROW LEVEL SECURITY;
DROP TABLE meta_app_registry_secrets;
DROP FUNCTION meta_app_registry_secret_guard();
DROP TABLE meta_app_registry;
