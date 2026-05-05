-- ==========================================
-- 000005_owner_role.up.sql
-- ==========================================

-- 1. РОЛЬ СУПЕРПОЛЬЗОВАТЕЛЯ: organization_id — nullable для owner
--    Существующие строки: role IN ('admin','member'), organization_id NOT NULL → OK

ALTER TABLE users
    ALTER COLUMN organization_id DROP NOT NULL;

-- 2. Обновить CHECK ролей пользователей
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_role_chk;

ALTER TABLE users
    ADD CONSTRAINT users_role_chk CHECK (role IN ('admin', 'member', 'owner')),
    ADD CONSTRAINT users_owner_org_chk CHECK (
        (role = 'owner' AND organization_id IS NULL) OR
        (role != 'owner' AND organization_id IS NOT NULL)
    );

COMMENT ON COLUMN users.role IS
    'Роль: admin — полный доступ к тенанту, member — работа с документами, owner — суперпользователь с кросс-тенантным доступом';
