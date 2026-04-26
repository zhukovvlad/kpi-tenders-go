DROP INDEX IF EXISTS idx_documents_artifact_kind;
ALTER TABLE document_tasks DROP COLUMN IF EXISTS input_storage_path;
