-- ==========================================
-- 000004_ui_layer.down.sql
-- ==========================================

-- 11. VIEW v_site_status
DROP VIEW IF EXISTS v_site_status;

-- 10. ТРИГГЕР last_activity_at
DROP TRIGGER IF EXISTS trg_extraction_requests_propagate_activity ON extraction_requests;
DROP TRIGGER IF EXISTS trg_documents_propagate_activity            ON documents;
DROP FUNCTION IF EXISTS propagate_site_activity();

-- 9. СЕССИИ СРАВНЕНИЯ
DROP TRIGGER IF EXISTS trg_immut_org_csd              ON comparison_session_documents;
DROP TRIGGER IF EXISTS trg_immut_org_comparison_sessions ON comparison_sessions;

DROP TABLE IF EXISTS comparison_session_documents;
DROP TABLE IF EXISTS comparison_sessions;

-- 8. ЛОГ СОБЫТИЙ ОБЪЕКТА
DROP TABLE IF EXISTS site_audit_log;

-- 7. ПРИГЛАШЕНИЯ ПОЛЬЗОВАТЕЛЕЙ
DROP TABLE IF EXISTS user_invitations;

-- 6. ПОЛЬЗОВАТЕЛИ
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_role_chk,
    DROP COLUMN IF EXISTS last_login_at;

-- 5. КЛЮЧИ ИЗВЛЕЧЕНИЯ
ALTER TABLE extraction_keys
    DROP COLUMN IF EXISTS display_name,
    DROP COLUMN IF EXISTS is_active,
    DROP COLUMN IF EXISTS category;

-- 4. ДОКУМЕНТЫ
DROP TRIGGER IF EXISTS trg_document_kind_role_org ON documents;

ALTER TABLE documents
    DROP CONSTRAINT IF EXISTS documents_bundle_self_ref_chk,
    DROP CONSTRAINT IF EXISTS documents_artifact_kind_role_chk,
    DROP COLUMN IF EXISTS bundle_id,
    DROP COLUMN IF EXISTS file_role_id,
    DROP COLUMN IF EXISTS contract_kind_id;

-- 3 + 2. СПРАВОЧНИКИ
DROP TRIGGER IF EXISTS trg_immut_org_file_roles   ON document_file_roles;
DROP TRIGGER IF EXISTS trg_immut_org_contract_kinds ON document_contract_kinds;

DROP FUNCTION IF EXISTS check_document_kind_role_org();

DROP TABLE IF EXISTS document_file_roles;
DROP TABLE IF EXISTS document_contract_kinds;

-- 1. ОБЪЕКТЫ СТРОИТЕЛЬСТВА
ALTER TABLE construction_sites
    DROP COLUMN IF EXISTS last_activity_at,
    DROP COLUMN IF EXISTS site_type,
    DROP COLUMN IF EXISTS cover_image_uploaded_at,
    DROP COLUMN IF EXISTS cover_image_path;
