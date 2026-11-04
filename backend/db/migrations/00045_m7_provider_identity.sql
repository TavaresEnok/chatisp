-- +goose Up
-- Logo do provedor (V1 simples, seção 12). O nome continua só em
-- tenant_retention_profiles.display_name. Esta tabela não guarda prazo,
-- automatic_purge nem URL remota. A imagem é bytea com teto de 256 KiB.

CREATE TABLE tenant_provider_logos (
    tenant_id uuid PRIMARY KEY REFERENCES tenants (id),
    content_type text NOT NULL,
    image bytea NOT NULL,
    width integer NOT NULL,
    height integer NOT NULL,
    byte_size integer NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tenant_provider_logos_type_chk CHECK (content_type IN ('image/png', 'image/jpeg')),
    CONSTRAINT tenant_provider_logos_bounds_chk CHECK (
        octet_length(image) BETWEEN 1 AND 262144
        AND byte_size = octet_length(image)
        AND width BETWEEN 1 AND 1024
        AND height BETWEEN 1 AND 1024
    )
);

ALTER TABLE tenant_provider_logos ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_provider_logos FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON tenant_provider_logos
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE tenant_provider_logos FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_provider_logos TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback descartaria o logo do provedor';
END $$;
-- +goose StatementEnd
