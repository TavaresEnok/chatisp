-- name: InsertTenant :one
INSERT INTO tenants (id, timezone, status, limits)
VALUES (
    sqlc.arg('id'),
    sqlc.arg('timezone'),
    sqlc.arg('status'),
    sqlc.arg('limits')
)
RETURNING id, timezone, status, limits, created_at, updated_at;

-- name: GetTenant :one
SELECT id, timezone, status, limits, created_at, updated_at
FROM tenants
WHERE id = sqlc.arg('id');
