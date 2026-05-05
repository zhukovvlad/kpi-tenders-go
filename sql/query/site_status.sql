-- name: GetSiteStatus :one
SELECT * FROM v_site_status
WHERE site_id = $1 AND organization_id = $2;

-- name: ListSiteStatusesByOrg :many
SELECT * FROM v_site_status
WHERE organization_id = $1;
