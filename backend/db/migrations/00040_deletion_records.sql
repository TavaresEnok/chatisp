-- +goose Up
-- Minimal ledger so a deletion made after a backup can be applied again on
-- the restored database before the service is reopened. This table does not
-- delete, expire or promise removal of an immutable backup copy.

CREATE TABLE deletion_records (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    subject text NOT NULL,
    subject_id uuid NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT deletion_records_subject_chk CHECK (subject IN ('attachment', 'credential_secret')),
    CONSTRAINT deletion_records_subject_key UNIQUE (tenant_id, subject, subject_id)
);

ALTER TABLE deletion_records ENABLE ROW LEVEL SECURITY;
ALTER TABLE deletion_records FORCE ROW LEVEL SECURITY;

CREATE POLICY deletion_records_tenant ON deletion_records
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE deletion_records FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT ON deletion_records TO chatisp_app;

-- +goose Down
DROP TABLE deletion_records;
