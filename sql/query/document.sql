-- name: CreateDocument :one
INSERT INTO documents (organization_id, site_id, uploaded_by, parent_id, file_name, storage_path, mime_type, file_size_bytes, artifact_kind)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- WARNING: This lookup is intentionally unscoped by organization_id.
-- Callers MUST enforce organization isolation at the service layer.
-- Prefer GetDocument when organization-scoped access is required.
-- name: GetDocumentByID :one
SELECT * FROM documents WHERE id = $1;

-- name: GetDocument :one
SELECT * FROM documents
WHERE id = $1 AND organization_id = $2;

-- name: ListRootDocumentsByOrganization :many
-- Только корневые документы (загруженные пользователем, не артефакты)
SELECT * FROM documents
WHERE organization_id = $1 AND parent_id IS NULL
ORDER BY created_at DESC;

-- name: ListRootDocumentsBySite :many
SELECT * FROM documents
WHERE organization_id = $1 AND site_id = $2 AND parent_id IS NULL
ORDER BY created_at DESC;

-- name: ListDocumentsByParent :many
-- Все артефакты, порождённые данным документом
SELECT * FROM documents
WHERE parent_id = $1
ORDER BY created_at ASC;

-- name: DeleteDocument :execrows
DELETE FROM documents WHERE id = $1 AND organization_id = $2;
