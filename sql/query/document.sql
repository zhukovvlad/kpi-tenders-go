-- name: CreateDocument :one
INSERT INTO documents (organization_id, project_id, title, file_path, status, uploaded_by)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetDocument :one
SELECT * FROM documents WHERE id = $1;

-- name: ListDocumentsByOrganization :many
SELECT * FROM documents
WHERE organization_id = $1
ORDER BY created_at DESC;

-- name: UpdateDocumentStatus :one
UPDATE documents SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteDocument :exec
DELETE FROM documents WHERE id = $1;

-- ── Document Tasks ──────────────────────────────────

-- name: CreateDocumentTask :one
INSERT INTO document_tasks (document_id, assigned_to, title, description, status, due_date)
VALUES ($1, $2, $3, $4, $5, $6)
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
