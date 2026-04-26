DROP INDEX IF EXISTS idx_documents_root;

ALTER TABLE documents
    DROP CONSTRAINT documents_parent_id_fkey,
    ADD CONSTRAINT documents_parent_id_fkey
        FOREIGN KEY (parent_id) REFERENCES documents(id) ON DELETE SET NULL;

ALTER TABLE documents DROP COLUMN artifact_kind;
