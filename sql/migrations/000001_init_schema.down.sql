DROP TRIGGER IF EXISTS trg_document_org_isolation ON documents;
DROP FUNCTION IF EXISTS check_document_org_isolation();
DROP TRIGGER IF EXISTS trg_site_org_isolation ON construction_sites;
DROP FUNCTION IF EXISTS check_site_org_isolation();

DROP TABLE IF EXISTS document_tasks;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS construction_sites;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;

DROP EXTENSION IF EXISTS pgcrypto;
