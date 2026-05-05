-- ==========================================
-- 000005_owner_role.down.sql
-- ==========================================

-- Pre-flight: abort if any owner users are still referenced by documents.uploaded_by
-- (NOT NULL FK). Remove or reassign owner-authored documents before rolling back.
DO $$
DECLARE
    blocking_count int;
BEGIN
    SELECT count(*) INTO blocking_count
    FROM documents d
    JOIN users u ON u.id = d.uploaded_by
    WHERE u.role = 'owner';
    IF blocking_count > 0 THEN
        RAISE EXCEPTION
            'Cannot roll back migration 000005: % document(s) still reference owner users '
            'via uploaded_by. Remove or reassign them first.',
            blocking_count;
    END IF;
END $$;

-- Delete owner users (safe after the FK pre-flight check above).
DELETE FROM users WHERE role = 'owner';

-- Restore original schema atomically: drop owner constraints, set NOT NULL,
-- reinstate the original two-role CHECK.
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_owner_org_chk,
    DROP CONSTRAINT IF EXISTS users_role_chk,
    ALTER COLUMN organization_id SET NOT NULL,
    ADD CONSTRAINT users_role_chk CHECK (role IN ('admin', 'member'));

COMMENT ON COLUMN users.role IS
    'Роль пользователя: admin — полный доступ к тенанту, member — работа с документами';
