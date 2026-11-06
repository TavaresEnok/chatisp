-- +goose Up
-- Durable cleanup after the subject erasure and its restore ledger commit.
CREATE TABLE privacy_object_cleanup (
    tenant_id uuid NOT NULL,
    contact_id uuid NOT NULL,
    attachment_id uuid NOT NULL,
    object_key text NOT NULL,
    object_version text NOT NULL,
    PRIMARY KEY (tenant_id, contact_id, attachment_id),
    FOREIGN KEY (tenant_id, contact_id) REFERENCES contacts (tenant_id, id)
);
ALTER TABLE privacy_object_cleanup ENABLE ROW LEVEL SECURITY;
ALTER TABLE privacy_object_cleanup FORCE ROW LEVEL SECURITY;
CREATE POLICY privacy_object_cleanup_tenant ON privacy_object_cleanup TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
REVOKE ALL ON privacy_object_cleanup FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT, DELETE ON privacy_object_cleanup TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback descartaria exclusões físicas pendentes';
END $$;
-- +goose StatementEnd
