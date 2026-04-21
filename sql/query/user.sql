-- name: CreateUser :one
INSERT INTO users (organization_id, email, password_hash, full_name, role)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: ListUsersByOrganization :many
SELECT id, organization_id, email, full_name, role, is_active, created_at, updated_at
FROM users
WHERE organization_id = $1
ORDER BY created_at ASC;

-- name: UpdateUser :one
UPDATE users
SET
    role      = COALESCE(sqlc.narg('role'), role),
    is_active = COALESCE(sqlc.narg('is_active'), is_active),
    updated_at = now()
WHERE id = $1 AND organization_id = $2
RETURNING id, organization_id, email, full_name, role, is_active, created_at, updated_at;
