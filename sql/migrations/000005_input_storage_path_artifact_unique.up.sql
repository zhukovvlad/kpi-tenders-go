-- Add input_storage_path to document_tasks.
-- For each module, this stores the correct file path to pass to the Python worker.
-- Convert tasks:   original document storage_path.
-- Anonymize tasks: convert_md artifact storage_path (set at task creation time).
-- This ensures the watchdog re-queues stale tasks with the correct file path
-- regardless of the module — instead of always joining documents.storage_path,
-- which returns the original PDF even for 'anonymize' tasks.
ALTER TABLE document_tasks
    ADD COLUMN input_storage_path TEXT NOT NULL DEFAULT '';

-- Back-fill existing 'convert' tasks: their correct input path is the parent document.
UPDATE document_tasks dt
SET input_storage_path = d.storage_path
FROM documents d
WHERE dt.document_id = d.id
  AND dt.module_name = 'convert';

-- Artifact idempotency: prevent duplicate artifacts of the same kind for the same parent.
-- Covers worker-retry scenarios where a second 'completed' callback would otherwise
-- insert a second artifact document.
CREATE UNIQUE INDEX idx_documents_artifact_kind
    ON documents (parent_id, artifact_kind)
    WHERE parent_id IS NOT NULL;
