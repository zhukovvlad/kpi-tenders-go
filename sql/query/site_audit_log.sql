-- name: CreateSiteAuditEvent :one
INSERT INTO site_audit_log (organization_id, site_id, actor_user_id, event_type, payload)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListSiteAuditLogBySite :many
SELECT * FROM site_audit_log
WHERE site_id = $1 AND organization_id = $2
ORDER BY created_at DESC
LIMIT $3 OFFSET $4;
