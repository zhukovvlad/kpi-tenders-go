ALTER TABLE document_tasks
    ADD COLUMN IF NOT EXISTS retry_count INT NOT NULL DEFAULT 0;

-- Partial index speeds up watchdog scans: only indexes rows where status = 'processing'.
CREATE INDEX IF NOT EXISTS idx_document_tasks_stale
    ON document_tasks (updated_at ASC)
    WHERE status = 'processing';
