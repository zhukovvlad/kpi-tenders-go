-- name: CreateDocument :one
INSERT INTO documents (organization_id, site_id, uploaded_by, parent_id, file_name, storage_path, mime_type, file_size_bytes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetDocument :one
SELECT * FROM documents WHERE id = $1;

-- name: ListDocumentsByOrganization :many
SELECT * FROM documents
WHERE organization_id = $1
ORDER BY created_at DESC;

-- name: ListDocumentsBySite :many
SELECT * FROM documents
WHERE site_id = $1
ORDER BY created_at DESC;

-- name: DeleteDocument :exec
DELETE FROM documents WHERE id = $1;

-- ── Document Tasks ──────────────────────────────────

-- name: CreateDocumentTask :one
INSERT INTO document_tasks (document_id, module_name)
VALUES ($1, $2)
RETURNING *;

-- name: GetDocumentTask :one
SELECT * FROM document_tasks WHERE id = $1;

-- name: ListTasksByDocument :many
SELECT * FROM document_tasks
WHERE document_id = $1
ORDER BY created_at DESC;

-- name: UpdateDocumentTaskStatus :one
UPDATE document_tasks SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteDocumentTask :exec
DELETE FROM document_tasks WHERE id = $1;
