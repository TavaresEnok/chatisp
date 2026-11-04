-- +goose Up
-- Aggregate columns for the private operational scrape. chatisp_platform may
-- read only closed states, ages and byte totals. Payload, error text, phone,
-- credential and remote origin stay revoked. No tenant label is produced here.

GRANT SELECT (state, created_at) ON outbound_attempts TO chatisp_platform;
GRANT SELECT (state) ON send_reservations TO chatisp_platform;
GRANT SELECT (queue_name, expires_at) ON tenant_job_reservations TO chatisp_platform;
GRANT SELECT (state, created_at) ON webhook_items TO chatisp_platform;
GRANT SELECT (connector_type, status) ON channel_connectors TO chatisp_platform;
GRANT SELECT (state) ON attachments TO chatisp_platform;

CREATE POLICY outbound_attempts_platform_ops ON outbound_attempts
    FOR SELECT
    TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform');

CREATE POLICY send_reservations_platform_ops ON send_reservations
    FOR SELECT
    TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform');

CREATE POLICY tenant_job_reservations_platform_ops ON tenant_job_reservations
    FOR SELECT
    TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform');

CREATE POLICY webhook_items_platform_ops ON webhook_items
    FOR SELECT
    TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform');

CREATE POLICY channel_connectors_platform_ops ON channel_connectors
    FOR SELECT
    TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform');

-- +goose Down
DROP POLICY IF EXISTS channel_connectors_platform_ops ON channel_connectors;
DROP POLICY IF EXISTS webhook_items_platform_ops ON webhook_items;
DROP POLICY IF EXISTS tenant_job_reservations_platform_ops ON tenant_job_reservations;
DROP POLICY IF EXISTS send_reservations_platform_ops ON send_reservations;
DROP POLICY IF EXISTS outbound_attempts_platform_ops ON outbound_attempts;

REVOKE SELECT (state) ON attachments FROM chatisp_platform;
REVOKE SELECT (connector_type, status) ON channel_connectors FROM chatisp_platform;
REVOKE SELECT (state, created_at) ON webhook_items FROM chatisp_platform;
REVOKE SELECT (queue_name, expires_at) ON tenant_job_reservations FROM chatisp_platform;
REVOKE SELECT (state) ON send_reservations FROM chatisp_platform;
REVOKE SELECT (state, created_at) ON outbound_attempts FROM chatisp_platform;
