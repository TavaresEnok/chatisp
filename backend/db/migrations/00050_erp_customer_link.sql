-- +goose Up
-- 08-abrir-cadastro: link to the provider's existing ERP screen.
-- The provider supplies the URL template; nothing here knows the supplier API.
-- The template is HTTPS, has exactly one {cliente} placeholder filled with the
-- external customer id, and never carries a credential or a CPF/CNPJ. The Go
-- validator is the authority; this CHECK is a backstop.

CREATE TABLE erp_customer_link_settings (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    url_template text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id),
    CONSTRAINT erp_customer_link_template_chk CHECK (
        char_length(url_template) BETWEEN 12 AND 512
        AND url_template ~ '^https://[^/@?#[:space:]]+'
        AND url_template !~ '[[:space:]]'
        AND (char_length(url_template) - char_length(replace(url_template, '{cliente}', ''))) = char_length('{cliente}')
    ),
    CONSTRAINT erp_customer_link_version_chk CHECK (version >= 1)
);

ALTER TABLE erp_customer_link_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE erp_customer_link_settings FORCE ROW LEVEL SECURITY;

CREATE POLICY erp_customer_link_settings_app_isolation ON erp_customer_link_settings
    FOR ALL TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

GRANT SELECT, INSERT, UPDATE, DELETE ON erp_customer_link_settings TO chatisp_app;

-- +goose Down
DROP TABLE IF EXISTS erp_customer_link_settings;
