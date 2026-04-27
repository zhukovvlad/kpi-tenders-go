DROP TRIGGER  IF EXISTS trg_immut_org_document_extracted_data ON document_extracted_data;
DROP TRIGGER  IF EXISTS trg_immut_org_extraction_keys         ON extraction_keys;
DROP FUNCTION IF EXISTS trg_prevent_org_change();
DROP TRIGGER  IF EXISTS trg_document_extracted_data_key_org ON document_extracted_data;
DROP FUNCTION IF EXISTS trg_check_extracted_data_key_org();
DROP TABLE IF EXISTS document_extracted_data;
DROP TABLE IF EXISTS extraction_keys;

ALTER TABLE documents DROP CONSTRAINT IF EXISTS uq_documents_id_org;
