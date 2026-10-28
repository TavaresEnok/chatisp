-- name: InsertUser :one
INSERT INTO users (id, email, status)
VALUES (
    sqlc.arg('id'),
    sqlc.arg('email'),
    sqlc.arg('status')
)
RETURNING id, email, status, created_at;
