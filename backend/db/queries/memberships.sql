-- name: InsertMembership :one
INSERT INTO memberships (id, tenant_id, user_id, role, status)
VALUES (
    sqlc.arg('id'),
    sqlc.arg('tenant_id'),
    sqlc.arg('user_id'),
    sqlc.arg('role'),
    sqlc.arg('status')
)
RETURNING id, tenant_id, user_id, role, status, created_at;

-- name: ListMembershipsByTenant :many
SELECT id, tenant_id, user_id, role, status, created_at
FROM memberships
WHERE tenant_id = sqlc.arg('tenant_id')
ORDER BY created_at, id;

-- name: UpdateMembershipStatus :execrows
UPDATE memberships
SET status = sqlc.arg('status')
WHERE tenant_id = sqlc.arg('tenant_id')
  AND id = sqlc.arg('id');
