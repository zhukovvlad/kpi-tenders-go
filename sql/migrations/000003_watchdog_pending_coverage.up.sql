DROP INDEX IF EXISTS idx_document_tasks_stale;

CREATE INDEX IF NOT EXISTS idx_document_tasks_stale
    ON document_tasks (updated_at ASC)
    WHERE status IN ('pending', 'processing');
