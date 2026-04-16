-- name: CreateOrganization :one
INSERT INTO organizations (name, inn)
VALUES ($1, $2)
RETURNING *;

-- name: GetOrganizationByID :one
SELECT * FROM organizations WHERE id = $1;

-- name: GetOrganizationByINN :one
SELECT * FROM organizations WHERE inn = $1;

-- name: UpdateOrganization :one
UPDATE organizations
SET name = $2, inn = $3, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteOrganization :execrows
DELETE FROM organizations WHERE id = $1;
