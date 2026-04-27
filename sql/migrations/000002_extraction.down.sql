DROP TABLE IF EXISTS document_extracted_data;
DROP TABLE IF EXISTS extraction_keys;

ALTER TABLE documents DROP CONSTRAINT IF EXISTS uq_documents_id_org;
