-- +goose Up
CREATE TABLE contact_consents (
 tenant_id uuid NOT NULL, id uuid NOT NULL, contact_id uuid NOT NULL, inbox_id uuid NOT NULL,
 purpose text NOT NULL CHECK (length(purpose) BETWEEN 1 AND 80 AND purpose=btrim(purpose)),
 source text NOT NULL CHECK (length(source) BETWEEN 1 AND 120),
 evidence text NOT NULL CHECK (length(evidence) BETWEEN 1 AND 500),
 granted_at timestamptz NOT NULL, revoked_at timestamptz,
 sequence bigint GENERATED ALWAYS AS IDENTITY,
 PRIMARY KEY(tenant_id,id),
 FOREIGN KEY(tenant_id,contact_id) REFERENCES contacts(tenant_id,id),
 FOREIGN KEY(tenant_id,inbox_id) REFERENCES inboxes(tenant_id,id)
);
CREATE INDEX contact_consents_scope ON contact_consents(tenant_id,contact_id,inbox_id,purpose,sequence DESC);
CREATE TABLE wa_templates (
 tenant_id uuid NOT NULL, waba_id uuid NOT NULL, external_id text NOT NULL CHECK(length(external_id) BETWEEN 1 AND 256),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 512), language text NOT NULL CHECK(length(language) BETWEEN 1 AND 40),
 category text NOT NULL CHECK(length(category) BETWEEN 1 AND 80), state text NOT NULL CHECK(length(state) BETWEEN 1 AND 80),
 components jsonb NOT NULL CHECK(jsonb_typeof(components)='array'),
 sync_source text NOT NULL CHECK(sync_source IN ('fake','driver')), synced_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,waba_id,external_id), UNIQUE(tenant_id,waba_id,name,language),
 FOREIGN KEY(tenant_id,waba_id) REFERENCES meta_wabas(tenant_id,id)
);
CREATE TABLE channel_policy_pending (
 tenant_id uuid NOT NULL, id uuid NOT NULL, contact_id uuid NOT NULL, inbox_id uuid NOT NULL,
 purpose text NOT NULL CHECK(length(purpose) BETWEEN 1 AND 80),
 state text NOT NULL CHECK(state IN ('pending','uncertain','dispatched','cancelled')),
 PRIMARY KEY(tenant_id,id),
 FOREIGN KEY(tenant_id,contact_id) REFERENCES contacts(tenant_id,id),
 FOREIGN KEY(tenant_id,inbox_id) REFERENCES inboxes(tenant_id,id)
);
-- Reservation commits before driver I/O. A newer request fences older responses.
CREATE TABLE channel_policy_catalog_sync (
 tenant_id uuid NOT NULL, waba_id uuid NOT NULL,
 requested_generation bigint NOT NULL CHECK(requested_generation > 0),
 applied_generation bigint NOT NULL DEFAULT 0 CHECK(applied_generation >= 0 AND applied_generation <= requested_generation),
 PRIMARY KEY(tenant_id,waba_id),
 FOREIGN KEY(tenant_id,waba_id) REFERENCES meta_wabas(tenant_id,id)
);
-- +goose StatementBegin
DO $$ DECLARE tab text; BEGIN
 FOREACH tab IN ARRAY ARRAY['contact_consents','wa_templates','channel_policy_pending','channel_policy_catalog_sync'] LOOP
  EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY',tab);
  EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY',tab);
  EXECUTE format('CREATE POLICY tenant_isolation ON %I TO chatisp_app USING (tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK (tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid)',tab);
 END LOOP;
END $$;
-- +goose StatementEnd
REVOKE ALL ON contact_consents,wa_templates,channel_policy_pending FROM PUBLIC,chatisp_app,chatisp_platform,chatisp_ingress;
REVOKE ALL ON channel_policy_catalog_sync FROM PUBLIC,chatisp_app,chatisp_platform,chatisp_ingress;
GRANT SELECT,INSERT,UPDATE ON channel_policy_catalog_sync TO chatisp_app;
REVOKE ALL ON SEQUENCE contact_consents_sequence_seq FROM PUBLIC,chatisp_app,chatisp_platform,chatisp_ingress;
GRANT SELECT,INSERT ON contact_consents TO chatisp_app;
GRANT USAGE ON SEQUENCE contact_consents_sequence_seq TO chatisp_app;
GRANT SELECT,INSERT,UPDATE,DELETE ON wa_templates TO chatisp_app;
GRANT SELECT,INSERT,UPDATE ON channel_policy_pending TO chatisp_app;
-- +goose Down
DROP TABLE channel_policy_catalog_sync;
DROP TABLE channel_policy_pending;
DROP TABLE wa_templates;
DROP TABLE contact_consents;
