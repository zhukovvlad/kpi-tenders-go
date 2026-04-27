-- Drop tables first (CASCADE removes their triggers automatically),
-- then drop the shared trigger function defined in this migration.
DROP TABLE IF EXISTS document_extracted_data CASCADE;
DROP TABLE IF EXISTS extraction_keys CASCADE;
DROP FUNCTION IF EXISTS trg_check_extracted_data_key_org();

ALTER TABLE documents DROP CONSTRAINT IF EXISTS uq_documents_id_org;
