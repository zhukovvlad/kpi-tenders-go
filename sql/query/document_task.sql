-- name: CreateDocumentTask :one
INSERT INTO document_tasks (document_id, module_name)
SELECT $1, $2
FROM documents
WHERE documents.id = $1 AND documents.organization_id = $3
RETURNING *;

-- name: GetDocumentTask :one
SELECT dt.id, dt.document_id, dt.module_name, dt.status, dt.celery_task_id,
       dt.result_payload, dt.error_message, dt.created_at, dt.updated_at
FROM document_tasks AS dt
JOIN documents AS d ON d.id = dt.document_id
WHERE dt.id = $1 AND d.organization_id = $2;

-- name: ListTasksByDocument :many
SELECT dt.id, dt.document_id, dt.module_name, dt.status, dt.celery_task_id,
       dt.result_payload, dt.error_message, dt.created_at, dt.updated_at
FROM document_tasks AS dt
JOIN documents AS d ON d.id = dt.document_id
WHERE dt.document_id = $1 AND d.organization_id = $2
ORDER BY dt.created_at DESC;

-- name: UpdateDocumentTaskStatus :one
UPDATE document_tasks
SET status = $3, updated_at = now()
WHERE document_tasks.id = $1
  AND document_tasks.document_id IN (
    SELECT documents.id FROM documents WHERE documents.organization_id = $2
  )
RETURNING *;

-- name: DeleteDocumentTask :execrows
DELETE FROM document_tasks
WHERE document_tasks.id = $1
  AND document_tasks.document_id IN (SELECT documents.id FROM documents WHERE documents.organization_id = $2);

-- name: UpdateWorkerTaskStatus :one
-- Internal: no org-check; callers must be authenticated via SERVICE_TOKEN.
UPDATE document_tasks
SET
    status         = $2,
    celery_task_id = COALESCE($3, celery_task_id),
    result_payload = COALESCE($4, result_payload),
    error_message  = COALESCE($5, error_message),
    updated_at     = now()
WHERE id = $1
RETURNING *;

-- name: CreateDocumentTaskInternal :one
-- Internal: creates a task directly by document_id without tenant org-check.
-- Use only from trusted internal paths (worker service); never expose publicly.
-- ON CONFLICT DO NOTHING makes this idempotent: duplicate (document_id, module_name)
-- returns pgx.ErrNoRows, which callers should treat as "task already exists".
INSERT INTO document_tasks (document_id, module_name)
VALUES ($1, $2)
ON CONFLICT (document_id, module_name) DO NOTHING
RETURNING *;

-- name: GetDocumentTaskByDocumentModule :one
-- Internal: find an existing task by (document_id, module_name) without org-check.
-- Returns the oldest task deterministically via ORDER BY.
SELECT * FROM document_tasks
WHERE document_id = $1 AND module_name = $2
ORDER BY created_at ASC, id ASC
LIMIT 1;
