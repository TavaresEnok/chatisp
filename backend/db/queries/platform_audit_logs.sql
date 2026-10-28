-- name: InsertPlatformAuditLog :one
INSERT INTO platform_audit_logs (
    id,
    actor,
    action,
    target_type,
    target_id,
    result,
    correlation_id
) VALUES (
    sqlc.arg('id'),
    sqlc.arg('actor'),
    sqlc.arg('action'),
    sqlc.arg('target_type'),
    sqlc.narg('target_id'),
    sqlc.arg('result'),
    sqlc.narg('correlation_id')
)
RETURNING id, actor, action, target_type, target_id, result, correlation_id, created_at;

-- name: ListPlatformAuditLogs :many
SELECT id, actor, action, target_type, target_id, result, correlation_id, created_at
FROM platform_audit_logs
ORDER BY created_at, id;
