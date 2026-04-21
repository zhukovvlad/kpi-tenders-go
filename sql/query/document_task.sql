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
