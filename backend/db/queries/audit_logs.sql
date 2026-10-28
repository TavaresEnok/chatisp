-- name: InsertAuditLog :one
INSERT INTO audit_logs (
    id,
    tenant_id,
    membership_id,
    actor_user_id,
    action,
    target_type,
    target_id,
    result,
    correlation_id
) VALUES (
    sqlc.arg('id'),
    sqlc.arg('tenant_id'),
    sqlc.narg('membership_id'),
    sqlc.narg('actor_user_id'),
    sqlc.arg('action'),
    sqlc.arg('target_type'),
    sqlc.narg('target_id'),
    sqlc.arg('result'),
    sqlc.narg('correlation_id')
)
RETURNING
    id,
    tenant_id,
    membership_id,
    actor_user_id,
    action,
    target_type,
    target_id,
    result,
    correlation_id,
    created_at;

-- name: ListAuditLogsByTenant :many
SELECT
    id,
    tenant_id,
    membership_id,
    actor_user_id,
    action,
    target_type,
    target_id,
    result,
    correlation_id,
    created_at
FROM audit_logs
WHERE tenant_id = sqlc.arg('tenant_id')
ORDER BY created_at, id;
