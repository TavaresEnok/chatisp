-- +goose Up
-- Platform usage counts. chatisp_platform may read only the columns needed
-- to count providers' technical usage. Message body, protocol, summary,
-- contact fields and attachment names stay revoked.

GRANT SELECT (tenant_id) ON inboxes TO chatisp_platform;
CREATE POLICY inboxes_platform_usage ON inboxes
    FOR SELECT
    TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform');

GRANT SELECT (tenant_id, status) ON conversations TO chatisp_platform;
CREATE POLICY conversations_platform_usage ON conversations
    FOR SELECT
    TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform');

GRANT SELECT (tenant_id, ingested_at) ON messages TO chatisp_platform;
CREATE POLICY messages_platform_usage ON messages
    FOR SELECT
    TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform');

GRANT SELECT (tenant_id, expected_bytes) ON attachments TO chatisp_platform;
CREATE POLICY attachments_platform_usage ON attachments
    FOR SELECT
    TO chatisp_platform
    USING (current_setting('app.access_scope', true) = 'platform');

-- +goose Down
DROP POLICY IF EXISTS attachments_platform_usage ON attachments;
REVOKE SELECT (tenant_id, expected_bytes) ON attachments FROM chatisp_platform;

DROP POLICY IF EXISTS messages_platform_usage ON messages;
REVOKE SELECT (tenant_id, ingested_at) ON messages FROM chatisp_platform;

DROP POLICY IF EXISTS conversations_platform_usage ON conversations;
REVOKE SELECT (tenant_id, status) ON conversations FROM chatisp_platform;

DROP POLICY IF EXISTS inboxes_platform_usage ON inboxes;
REVOKE SELECT (tenant_id) ON inboxes FROM chatisp_platform;
