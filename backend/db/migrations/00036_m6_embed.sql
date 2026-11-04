-- +goose Up
-- Painel externo por provedor. A URL e a origem ficam no tenant;
-- o iframe não guarda mensagem, documento nem segredo.
CREATE TABLE provider_embed_settings (
    tenant_id uuid PRIMARY KEY,
    version bigint NOT NULL CHECK (version > 0),
    allowed_origin text NOT NULL CHECK (char_length(allowed_origin) BETWEEN 1 AND 256),
    panel_url text NOT NULL CHECK (char_length(panel_url) BETWEEN 1 AND 2048),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT provider_embed_settings_tenant_fkey FOREIGN KEY (tenant_id) REFERENCES tenants (id)
);

ALTER TABLE provider_embed_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE provider_embed_settings FORCE ROW LEVEL SECURITY;

CREATE POLICY provider_embed_settings_app_isolation ON provider_embed_settings
    FOR ALL
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE provider_embed_settings FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT, UPDATE, DELETE ON provider_embed_settings TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback descartaria a configuração do painel externo';
END $$;
-- +goose StatementEnd
