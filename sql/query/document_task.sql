-- name: CreateDocumentTask :one
INSERT INTO document_tasks (document_id, module_name)
SELECT $1, $2
FROM documents
WHERE documents.id = $1 AND documents.organization_id = $3
RETURNING *;

-- name: GetDocumentTask :one
SELECT dt.*
FROM document_tasks AS dt
JOIN documents AS d ON d.id = dt.document_id
WHERE dt.id = $1 AND d.organization_id = $2;

-- name: ListTasksByDocument :many
SELECT dt.*
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

-- name: ListStaleTasks :many
-- Watchdog: returns stuck tasks (pending or processing) whose updated_at is older than $1
-- (cutoff timestamp), joined with their document's storage_path for re-queuing.
-- Covers two failure modes: worker died mid-processing (processing) and
-- Redis message was lost before worker picked it up (pending).
-- No org-check; caller must be trusted (watchdog goroutine only).
SELECT dt.id,
       dt.document_id,
       dt.module_name,
       dt.retry_count,
       d.storage_path
FROM document_tasks dt
JOIN documents d ON d.id = dt.document_id
WHERE dt.status IN ('pending', 'processing')
  AND dt.updated_at < $1
ORDER BY dt.updated_at ASC;

-- name: MarkStaleTaskPending :execrows
-- Watchdog: atomically resets a stale task to pending and increments retry_count.
-- The WHERE status IN (...) guard makes this a compare-and-swap, so two
-- concurrent watchdog instances cannot double-claim the same task.
UPDATE document_tasks
SET status      = 'pending',
    retry_count = retry_count + 1,
    updated_at  = now()
WHERE id     = $1
  AND status IN ('pending', 'processing');

-- name: MarkStaleTaskFailed :execrows
-- Watchdog: permanently fails a task that has exhausted all retry attempts.
UPDATE document_tasks
SET status        = 'failed',
    error_message = 'stale task: exceeded max retry attempts',
    updated_at    = now()
WHERE id     = $1
  AND status IN ('pending', 'processing');
