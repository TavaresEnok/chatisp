-- +goose Up
-- Alertas operacionais locais do provedor. A linha fica no banco e o admin
-- a lê no painel. Não envia e-mail, não chama webhook externo, não mede carga
-- e não restaura banco, anexo ou chave.

CREATE TABLE ops_alert_settings (
    tenant_id uuid PRIMARY KEY REFERENCES tenants (id),
    job_age_seconds integer NOT NULL CHECK (job_age_seconds BETWEEN 60 AND 604800),
    webhook_age_seconds integer NOT NULL CHECK (webhook_age_seconds BETWEEN 60 AND 604800),
    version bigint NOT NULL CHECK (version > 0)
);

CREATE TABLE ops_alerts (
    tenant_id uuid NOT NULL REFERENCES tenants (id),
    id uuid NOT NULL,
    signal text NOT NULL,
    subject_key text NOT NULL,
    label text NOT NULL,
    opened_at timestamptz NOT NULL,
    resolved_at timestamptz,
    PRIMARY KEY (tenant_id, id),
    CONSTRAINT ops_alerts_signal_chk CHECK (
        signal IN ('job_age', 'connector_disconnected', 'webhook_incomplete')
    ),
    CONSTRAINT ops_alerts_subject_chk CHECK (
        char_length(subject_key) BETWEEN 1 AND 80
        AND subject_key = btrim(subject_key)
    ),
    CONSTRAINT ops_alerts_label_chk CHECK (
        char_length(label) BETWEEN 1 AND 180
        AND label = btrim(label)
    ),
    CONSTRAINT ops_alerts_subject_key UNIQUE (tenant_id, signal, subject_key)
);

ALTER TABLE ops_alert_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops_alert_settings FORCE ROW LEVEL SECURITY;
ALTER TABLE ops_alerts ENABLE ROW LEVEL SECURITY;
ALTER TABLE ops_alerts FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON ops_alert_settings
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE POLICY tenant_isolation ON ops_alerts
    TO chatisp_app
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

REVOKE ALL ON TABLE ops_alert_settings FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
REVOKE ALL ON TABLE ops_alerts FROM PUBLIC, chatisp_app, chatisp_platform, chatisp_ingress;
GRANT SELECT, INSERT, UPDATE ON TABLE ops_alert_settings TO chatisp_app;
GRANT SELECT, INSERT, UPDATE ON TABLE ops_alerts TO chatisp_app;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    RAISE EXCEPTION 'rollback apagaria o registro local de alertas operacionais';
END $$;
-- +goose StatementEnd
