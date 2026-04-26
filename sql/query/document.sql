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
-- Все артефакты, порождённые данным документом; scoped by organization_id for tenant isolation.
SELECT * FROM documents
WHERE parent_id = $1 AND organization_id = $2
ORDER BY created_at ASC;

-- name: DeleteDocument :execrows
DELETE FROM documents WHERE id = $1 AND organization_id = $2;

-- name: CreateArtifactDocument :one
-- Idempotent artifact creation: on conflict (parent_id, artifact_kind) updates
-- artifact metadata from the latest callback so that RETURNING yields the current row state.
-- Prevents duplicate artifact documents when a worker sends a duplicate 'completed' callback.
INSERT INTO documents (organization_id, site_id, uploaded_by, parent_id, file_name, storage_path, mime_type, file_size_bytes, artifact_kind)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (parent_id, artifact_kind) WHERE parent_id IS NOT NULL
DO UPDATE SET
    file_name       = EXCLUDED.file_name,
    storage_path    = EXCLUDED.storage_path,
    mime_type       = EXCLUDED.mime_type,
    file_size_bytes = EXCLUDED.file_size_bytes
RETURNING *;
