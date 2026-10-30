-- +goose Up
CREATE TABLE notification_preferences (
    tenant_id uuid NOT NULL,
    membership_id uuid NOT NULL,
    sound boolean NOT NULL DEFAULT true,
    browser boolean NOT NULL DEFAULT false,
    silent boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, membership_id),
    FOREIGN KEY (tenant_id, membership_id) REFERENCES memberships (tenant_id, id)
);
ALTER TABLE notification_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_preferences FORCE ROW LEVEL SECURITY;
CREATE POLICY notification_preferences_tenant ON notification_preferences TO chatisp_app
    USING (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid)
    WITH CHECK (tenant_id = nullif(current_setting('app.tenant_id', true), '')::uuid);
GRANT SELECT, INSERT, UPDATE ON notification_preferences TO chatisp_app;

-- +goose Down
DROP TABLE notification_preferences;
