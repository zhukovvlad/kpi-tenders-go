-- Удаление триггеров запрета смены organization_id
DROP TRIGGER IF EXISTS trg_extraction_keys_prevent_org_change ON extraction_keys;
DROP TRIGGER IF EXISTS trg_documents_prevent_org_change ON documents;
DROP TRIGGER IF EXISTS trg_sites_prevent_org_change ON construction_sites;
DROP TRIGGER IF EXISTS trg_users_prevent_org_change ON users;
DROP FUNCTION IF EXISTS prevent_organization_id_change();

-- Удаление триггеров tenant isolation
DROP TRIGGER IF EXISTS trg_document_org_isolation ON documents;
DROP FUNCTION IF EXISTS check_document_org_isolation();
DROP TRIGGER IF EXISTS trg_site_org_isolation ON construction_sites;
DROP FUNCTION IF EXISTS check_site_org_isolation();

-- Удаление таблиц в обратном порядке (зависимости)
DROP TABLE IF EXISTS document_extracted_data;
DROP TABLE IF EXISTS document_tasks;
DROP TABLE IF EXISTS extraction_keys;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS construction_sites;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;

DROP EXTENSION IF EXISTS pgcrypto;
