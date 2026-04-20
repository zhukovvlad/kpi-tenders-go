-- name: CreateDocument :one
INSERT INTO documents (organization_id, site_id, uploaded_by, parent_id, file_name, storage_path, mime_type, file_size_bytes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetDocument :one
SELECT * FROM documents
WHERE id = $1 AND organization_id = $2;

-- name: ListDocumentsByOrganization :many
SELECT * FROM documents
WHERE organization_id = $1
ORDER BY created_at DESC;

-- name: ListDocumentsBySite :many
SELECT * FROM documents
WHERE organization_id = $1 AND site_id = $2
ORDER BY created_at DESC;

-- name: DeleteDocument :execrows
DELETE FROM documents WHERE id = $1 AND organization_id = $2;

-- ── Document Tasks ──────────────────────────────────

-- name: CreateDocumentTask :one
INSERT INTO document_tasks (document_id, module_name)
VALUES ($1, $2)
RETURNING *;

-- name: GetDocumentTask :one
SELECT dt.id, dt.document_id, dt.module_name, dt.status, dt.celery_task_id,
       dt.result_payload, dt.error_message, dt.created_at, dt.updated_at
FROM document_tasks AS dt
JOIN documents AS d ON d.id = dt.document_id
WHERE dt.id = $1 AND d.organization_id = $2;

-- name: ListTasksByDocument :many
SELECT * FROM document_tasks
WHERE document_id = $1
ORDER BY created_at DESC;

-- name: UpdateDocumentTaskStatus :one
UPDATE document_tasks SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteDocumentTask :execrows
DELETE FROM document_tasks
WHERE document_tasks.id = $1
  AND document_tasks.document_id IN (SELECT documents.id FROM documents WHERE documents.organization_id = $2);
