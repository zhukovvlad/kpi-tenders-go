DROP INDEX IF EXISTS idx_document_tasks_stale;

ALTER TABLE document_tasks
    DROP COLUMN IF EXISTS retry_count;
