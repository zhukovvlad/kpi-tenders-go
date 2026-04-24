-- Enforce one task per (document, module) pair.
-- This makes CreateDocumentTaskInternal idempotent via ON CONFLICT DO NOTHING.
ALTER TABLE document_tasks
    ADD CONSTRAINT uq_document_tasks_document_module UNIQUE (document_id, module_name);
