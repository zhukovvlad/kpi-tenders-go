-- name: ListFileRolesByOrg :many
-- Returns all file roles visible to the given tenant: org-specific AND system roles (organization_id IS NULL).
SELECT * FROM document_file_roles
WHERE organization_id = $1::uuid OR organization_id IS NULL
ORDER BY sort_order ASC, display_name ASC;

-- name: GetFileRole :one
SELECT * FROM document_file_roles
WHERE id = $1
  AND (organization_id = $2::uuid OR organization_id IS NULL);

-- name: CreateFileRole :one
INSERT INTO document_file_roles (organization_id, display_name, sort_order, is_active)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateFileRole :one
UPDATE document_file_roles
SET display_name = $3,
    sort_order   = $4,
    is_active    = $5,
    updated_at   = now()
WHERE id = $1 AND organization_id = $2
RETURNING *;

-- name: DeleteFileRole :execrows
DELETE FROM document_file_roles
WHERE id = $1 AND organization_id = $2;
