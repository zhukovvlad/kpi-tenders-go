DROP INDEX IF EXISTS idx_document_tasks_extraction_request_id;
ALTER TABLE document_tasks DROP CONSTRAINT IF EXISTS document_tasks_module_request_chk;
DROP INDEX IF EXISTS uq_document_tasks_request_module;
DROP INDEX IF EXISTS uq_document_tasks_doc_singleton;

ALTER TABLE document_tasks
    ADD CONSTRAINT uq_document_tasks_document_module UNIQUE (document_id, module_name);

ALTER TABLE document_tasks DROP COLUMN IF EXISTS extraction_request_id;

DROP TRIGGER IF EXISTS trg_immut_org_extraction_requests ON extraction_requests;
DROP INDEX IF EXISTS idx_extraction_requests_doc_pending;
DROP INDEX IF EXISTS idx_extraction_requests_document_id;
DROP TABLE IF EXISTS extraction_requests;
