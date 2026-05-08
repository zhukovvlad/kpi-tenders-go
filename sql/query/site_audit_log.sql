-- name: CreateSiteAuditEvent :one
INSERT INTO site_audit_log (organization_id, site_id, actor_user_id, event_type, payload)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: ListSiteAuditLogBySite :many
SELECT * FROM site_audit_log
WHERE site_id = $1 AND organization_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3 OFFSET $4;

-- name: ListSiteEventsBySite :many
-- Returns audit events with actor_name resolved from users table.
SELECT
    sal.id,
    sal.site_id,
    sal.organization_id,
    sal.actor_user_id,
    sal.event_type,
    sal.payload,
    sal.created_at,
    COALESCE(u.first_name || ' ' || u.last_name, 'Система') AS actor_name
FROM site_audit_log sal
LEFT JOIN users u ON sal.actor_user_id = u.id
WHERE sal.site_id = sqlc.arg(site_id)
  AND sal.organization_id = sqlc.arg(organization_id)
ORDER BY sal.created_at DESC, sal.id DESC
LIMIT sqlc.arg(limit_)
OFFSET sqlc.arg(offset_);
