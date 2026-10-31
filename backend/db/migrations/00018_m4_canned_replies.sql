-- +goose Up
CREATE TABLE canned_replies (
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 id uuid NOT NULL,
 shortcode text NOT NULL CHECK (shortcode ~ '^[a-z0-9][a-z0-9_-]{0,63}$'),
 title text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 120),
 content text NOT NULL CHECK (length(btrim(content)) BETWEEN 1 AND 10000),
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 PRIMARY KEY (tenant_id,id), UNIQUE (tenant_id,shortcode)
);
CREATE TABLE attendant_signature_settings (
 tenant_id uuid PRIMARY KEY REFERENCES tenants(id),
 enabled boolean NOT NULL DEFAULT false,
 prefix text NOT NULL DEFAULT '{{nome_atendente}}: ' CHECK (length(prefix) BETWEEN 1 AND 240),
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0)
);
-- Durable acceptance receipt: survives template/config changes and retries.
CREATE TABLE attendant_signature_receipts (
 tenant_id uuid NOT NULL REFERENCES tenants(id),
 acceptance_id uuid NOT NULL,
 membership_id uuid NOT NULL,
 original_text text NOT NULL,
 accepted_text text NOT NULL,
 PRIMARY KEY (tenant_id,acceptance_id),
 FOREIGN KEY (tenant_id,membership_id) REFERENCES memberships(tenant_id,id)
);
ALTER TABLE canned_replies ENABLE ROW LEVEL SECURITY;
ALTER TABLE canned_replies FORCE ROW LEVEL SECURITY;
ALTER TABLE attendant_signature_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE attendant_signature_settings FORCE ROW LEVEL SECURITY;
ALTER TABLE attendant_signature_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE attendant_signature_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY canned_replies_tenant ON canned_replies TO chatisp_app
 USING (tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid);
CREATE POLICY attendant_signature_settings_tenant ON attendant_signature_settings TO chatisp_app
 USING (tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid);
CREATE POLICY attendant_signature_receipts_tenant ON attendant_signature_receipts TO chatisp_app
 USING (tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid)
 WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT,INSERT,UPDATE,DELETE ON canned_replies TO chatisp_app;
GRANT SELECT,INSERT,UPDATE ON attendant_signature_settings TO chatisp_app;
GRANT SELECT,INSERT ON attendant_signature_receipts TO chatisp_app;

-- +goose Down
DROP TABLE attendant_signature_receipts;
DROP TABLE attendant_signature_settings;
DROP TABLE canned_replies;
