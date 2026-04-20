-- name: CreateConstructionSite :one
INSERT INTO construction_sites (organization_id, parent_id, name, status, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetConstructionSite :one
SELECT * FROM construction_sites WHERE id = $1;

-- name: ListConstructionSitesByOrganization :many
SELECT * FROM construction_sites
WHERE organization_id = $1
ORDER BY created_at DESC;

-- name: UpdateConstructionSite :one
UPDATE construction_sites
SET name       = $2,
    status     = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteConstructionSite :execrows
DELETE FROM construction_sites WHERE id = $1;
