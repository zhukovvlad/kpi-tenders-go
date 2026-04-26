-- name: CreateDocumentTask :one
-- Public API: only 'convert' tasks may be created here; the input is always the
-- original document's storage_path. Other modules (e.g. anonymize) read a derived
-- artifact path and must be created internally via CreateDocumentTaskInternal.
-- Callers MUST map pgx.ErrNoRows to 404: the INSERT ... SELECT returns no rows
-- when the document is missing or belongs to another organization, and this is
-- intentionally reported as 404 to avoid leaking tenant existence. This is
-- distinct from unique-constraint violations on (document_id, module_name).
INSERT INTO document_tasks (document_id, module_name, input_storage_path)
SELECT $1, $2, d.storage_path
FROM documents d
WHERE d.id = $1
  AND d.organization_id = $3
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
-- $3 is input_storage_path: the file path the Python worker will receive for this module.
INSERT INTO document_tasks (document_id, module_name, input_storage_path)
VALUES ($1, $2, $3)
ON CONFLICT (document_id, module_name) DO NOTHING
RETURNING *;

-- name: UpdateTaskResultPayload :one
-- Internal: no org-check; callers must be authenticated via SERVICE_TOKEN.
-- Updates result_payload only; does not change status, celery_task_id, or error_message.
-- Used by WorkerService after registering artifacts so updated_at is touched
-- only for payload changes, not for status semantics.
UPDATE document_tasks
SET result_payload = $2,
    updated_at     = now()
WHERE id = $1
RETURNING *;

-- name: ListStaleTasks :many
-- Watchdog: returns stuck tasks (pending or processing) whose updated_at is older than $1
-- (cutoff timestamp). Uses dt.input_storage_path so the correct file path is returned
-- for every module: 'convert' tasks get the original document path, 'anonymize' tasks
-- get the convert_md artifact path (stored at task creation time).
-- Covers two failure modes: worker died mid-processing (processing) and
-- Redis message was lost before worker picked it up (pending).
-- No org-check; caller must be trusted (watchdog goroutine only).
SELECT dt.id,
       dt.document_id,
       dt.module_name,
       dt.retry_count,
       dt.input_storage_path
FROM document_tasks dt
WHERE dt.status IN ('pending', 'processing')
  AND dt.updated_at < sqlc.arg(cutoff)
ORDER BY dt.updated_at ASC
LIMIT sqlc.arg(batch_size);

-- name: MarkStaleTaskPending :execrows
-- Watchdog: atomically resets a stale task to pending and increments retry_count.
-- The WHERE status IN (...) AND updated_at < cutoff guard makes this a true
-- compare-and-swap on both status and staleness: two concurrent watchdog instances
-- cannot double-claim the same task, and a task that was refreshed between
-- ListStaleTasks and this UPDATE will not be incorrectly reset.
UPDATE document_tasks
SET status         = 'pending',
    retry_count    = retry_count + 1,
    celery_task_id = NULL,
    error_message  = NULL,
    updated_at     = now()
WHERE id        = $1
  AND status IN ('pending', 'processing')
  AND updated_at < sqlc.arg(cutoff);

-- name: MarkStaleTaskFailed :execrows
-- Watchdog: permanently fails a task that has exhausted all retry attempts.
-- updated_at < cutoff prevents failing a task that was refreshed after ListStaleTasks.
UPDATE document_tasks
SET status        = 'failed',
    error_message = sqlc.arg(error_message),
    updated_at    = now()
WHERE id        = $1
  AND status IN ('pending', 'processing')
  AND updated_at < sqlc.arg(cutoff);
