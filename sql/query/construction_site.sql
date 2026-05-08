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
    cover_image_uploaded_at = CASE WHEN $3 IS NOT NULL THEN now() ELSE cover_image_uploaded_at END,
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

-- name: GetSiteAncestors :many
-- Returns ancestors of a site ordered from root to the site itself.
WITH RECURSIVE ancestry AS (
    SELECT cs.id, cs.name, cs.parent_id, 0 AS depth
    FROM construction_sites cs
    WHERE cs.id = sqlc.arg(site_id) AND cs.organization_id = sqlc.arg(organization_id)
    UNION ALL
    SELECT p.id, p.name, p.parent_id, a.depth + 1
    FROM construction_sites p
    JOIN ancestry a ON p.id = a.parent_id
    WHERE p.organization_id = a.organization_id
)
SELECT name FROM ancestry ORDER BY depth DESC;

-- name: ListSiteExtractedCounts :many
-- Returns extracted parameter count per site for a given list of site IDs.
SELECT d.site_id, COUNT(ded.id)::bigint AS extracted_count
FROM documents d
JOIN document_extracted_data ded
    ON ded.document_id = d.id AND ded.organization_id = d.organization_id
WHERE d.organization_id = sqlc.arg(organization_id)
  AND d.site_id = ANY(sqlc.arg(site_ids)::uuid[])
  AND d.parent_id IS NULL
GROUP BY d.site_id;

-- name: ListSiteContractKinds :many
-- Returns distinct contract kinds for documents in each of the given sites.
SELECT d.site_id, ck.id, ck.display_name, ck.is_active
FROM documents d
JOIN document_contract_kinds ck ON ck.id = d.contract_kind_id
WHERE d.organization_id = sqlc.arg(organization_id)
  AND d.site_id = ANY(sqlc.arg(site_ids)::uuid[])
  AND d.parent_id IS NULL
  AND d.contract_kind_id IS NOT NULL
GROUP BY d.site_id, ck.id, ck.display_name, ck.is_active;
