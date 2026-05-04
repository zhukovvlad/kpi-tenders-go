-- name: CreateConstructionSite :one
INSERT INTO construction_sites (organization_id, parent_id, name, status, created_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetConstructionSite :one
SELECT * FROM construction_sites
WHERE id = $1 AND organization_id = $2;

-- name: ListConstructionSitesByOrganization :many
SELECT * FROM construction_sites
WHERE organization_id = $1
ORDER BY created_at DESC;

-- name: UpdateConstructionSite :one
UPDATE construction_sites
SET name       = $3,
    status     = $4,
    updated_at = now()
WHERE id = $1 AND organization_id = $2
RETURNING *;

-- name: UpdateConstructionSiteCover :one
UPDATE construction_sites
SET cover_image_path        = $3,
    cover_image_uploaded_at = now(),
    updated_at              = now()
WHERE id = $1 AND organization_id = $2
RETURNING *;

-- name: UpdateConstructionSiteType :one
UPDATE construction_sites
SET site_type  = $3,
    updated_at = now()
WHERE id = $1 AND organization_id = $2
RETURNING *;

-- name: ListConstructionSitesByParent :many
SELECT * FROM construction_sites
WHERE organization_id = $1 AND parent_id = $2
ORDER BY last_activity_at DESC;

-- name: ListRootConstructionSites :many
SELECT * FROM construction_sites
WHERE organization_id = $1 AND parent_id IS NULL
ORDER BY last_activity_at DESC;

-- name: DeleteConstructionSite :execrows
DELETE FROM construction_sites WHERE id = $1 AND organization_id = $2;
