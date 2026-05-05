-- ==========================================
-- 000005_owner_role.down.sql
-- ==========================================

-- Удалить owner-пользователей перед восстановлением NOT NULL
DELETE FROM users WHERE role = 'owner';

-- Удалить owner-специфичные ограничения
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_owner_org_chk,
    DROP CONSTRAINT IF EXISTS users_role_chk;

-- Вернуть NOT NULL и CHECK только с admin/member
ALTER TABLE users
    ALTER COLUMN organization_id SET NOT NULL,
    ADD CONSTRAINT users_role_chk CHECK (role IN ('admin', 'member'));

COMMENT ON COLUMN users.role IS
    'Роль пользователя: admin — полный доступ к тенанту, member — работа с документами';
