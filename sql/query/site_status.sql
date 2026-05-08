-- name: GetSiteStatus :one
SELECT * FROM v_site_status
WHERE site_id = $1 AND organization_id = $2;

-- name: ListSiteStatusesByOrg :many
SELECT * FROM v_site_status
WHERE organization_id = $1;

-- name: ListSiteStatusesBySiteIds :many
SELECT * FROM v_site_status
WHERE organization_id = sqlc.arg(organization_id)
  AND site_id = ANY(sqlc.arg(site_ids)::uuid[]);
