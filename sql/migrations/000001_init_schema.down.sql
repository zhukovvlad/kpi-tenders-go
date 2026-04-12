-- 000001_init_schema.down.sql

DROP TABLE IF EXISTS document_tasks;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS organizations;

DROP EXTENSION IF EXISTS "vector";
DROP EXTENSION IF EXISTS "uuid-ossp";
