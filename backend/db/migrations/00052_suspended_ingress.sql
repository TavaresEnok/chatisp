-- +goose Up
-- Admission is bounded explicitly by the installation. Holds never replay
-- automatically on reactivation: reconciliation requires operator review.
CREATE TABLE suspended_ingress_holds (
    tenant_id uuid NOT NULL REFERENCES tenants(id),
    app_id text NOT NULL,
    body_sha256 bytea NOT NULL CHECK (octet_length(body_sha256) = 32),
    body_bytes bigint NOT NULL CHECK (body_bytes > 0),
    held_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, app_id, body_sha256)
);
CREATE TABLE suspended_ingress_alerts (
    tenant_id uuid PRIMARY KEY REFERENCES tenants(id),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    rejected_count bigint NOT NULL DEFAULT 0 CHECK (rejected_count >= 0),
    admission_blocked boolean NOT NULL DEFAULT false
);
ALTER TABLE suspended_ingress_holds ENABLE ROW LEVEL SECURITY;
ALTER TABLE suspended_ingress_holds FORCE ROW LEVEL SECURITY;
ALTER TABLE suspended_ingress_alerts ENABLE ROW LEVEL SECURITY;
ALTER TABLE suspended_ingress_alerts FORCE ROW LEVEL SECURITY;
CREATE POLICY ingress ON suspended_ingress_holds TO chatisp_ingress
    USING (current_setting('app.access_scope', true) = 'ingress')
    WITH CHECK (current_setting('app.access_scope', true) = 'ingress');
CREATE POLICY ingress ON suspended_ingress_alerts TO chatisp_ingress
    USING (current_setting('app.access_scope', true) = 'ingress')
    WITH CHECK (current_setting('app.access_scope', true) = 'ingress');
CREATE POLICY tenant_read ON suspended_ingress_holds FOR SELECT TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_read ON suspended_ingress_alerts FOR SELECT TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY platform_read ON suspended_ingress_holds FOR SELECT TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform');
CREATE POLICY platform_read ON suspended_ingress_alerts FOR SELECT TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform');
REVOKE ALL ON suspended_ingress_holds, suspended_ingress_alerts FROM PUBLIC;
GRANT SELECT, INSERT ON suspended_ingress_holds TO chatisp_ingress;
GRANT SELECT, INSERT, UPDATE ON suspended_ingress_alerts TO chatisp_ingress;
GRANT SELECT ON suspended_ingress_holds, suspended_ingress_alerts TO chatisp_app, chatisp_platform;
-- Only status/id, never tenant settings or credentials, is visible to ingress.
GRANT SELECT (id, status) ON tenants TO chatisp_ingress;
CREATE POLICY tenants_ingress_status ON tenants FOR SELECT TO chatisp_ingress
    USING (current_setting('app.access_scope', true) = 'ingress');
ALTER TABLE ops_alerts DROP CONSTRAINT ops_alerts_signal_chk;
ALTER TABLE ops_alerts ADD CONSTRAINT ops_alerts_signal_chk CHECK (
    signal IN ('job_age', 'connector_disconnected', 'webhook_incomplete', 'suspended_ingress')
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback apagaria retenção e alertas de suspensão';
END $$;
-- +goose StatementEnd
