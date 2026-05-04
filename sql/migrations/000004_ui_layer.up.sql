-- ==========================================
-- 000004_ui_layer.up.sql
-- ==========================================

-- ==========================================
-- 1. ОБЪЕКТЫ СТРОИТЕЛЬСТВА: обложка, тип, свежесть
-- ==========================================

ALTER TABLE construction_sites
    ADD COLUMN cover_image_path        TEXT,
    ADD COLUMN cover_image_uploaded_at TIMESTAMPTZ,
    ADD COLUMN site_type               VARCHAR(50)
        CHECK (site_type IN (
            'residential_complex', 'business_center', 'warehouse',
            'phase', 'building', 'section', 'other'
        )),
    ADD COLUMN last_activity_at        TIMESTAMPTZ NOT NULL DEFAULT now();

COMMENT ON COLUMN construction_sites.cover_image_path        IS 'Путь к обложке объекта в MinIO; NULL — плейсхолдер';
COMMENT ON COLUMN construction_sites.cover_image_uploaded_at IS 'Время последней загрузки обложки';
COMMENT ON COLUMN construction_sites.site_type               IS 'Тип объекта: residential_complex | business_center | warehouse | phase | building | section | other; NULL если не указан';
COMMENT ON COLUMN construction_sites.last_activity_at        IS 'Время последней активности в объекте или любом его потомке; обновляется триггером';


-- ==========================================
-- 2. СПРАВОЧНИК ТИПОВ ДОГОВОРОВ
-- ==========================================

CREATE TABLE document_contract_kinds (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID         REFERENCES organizations(id) ON DELETE CASCADE,
    display_name    VARCHAR(150) NOT NULL,
    sort_order      SMALLINT     NOT NULL DEFAULT 0,
    is_active       BOOLEAN      NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT uq_contract_kinds_org_name
        UNIQUE NULLS NOT DISTINCT (organization_id, display_name)
);

CREATE INDEX idx_contract_kinds_org_id ON document_contract_kinds(organization_id);

COMMENT ON TABLE  document_contract_kinds                 IS 'Справочник типов договоров; управляется тенантом';
COMMENT ON COLUMN document_contract_kinds.organization_id IS 'NULL — системная запись, видна всем тенантам; NOT NULL — запись конкретного тенанта';
COMMENT ON COLUMN document_contract_kinds.display_name    IS 'Человекочитаемое название: «Генподряд», «Стройконтроль», «Отделка» …';
COMMENT ON COLUMN document_contract_kinds.sort_order      IS 'Порядок отображения в UI; меньше — выше';
COMMENT ON COLUMN document_contract_kinds.is_active       IS 'false — тип скрыт из выпадающих списков; существующие документы не затрагиваются';


-- ==========================================
-- 3. СПРАВОЧНИК РОЛЕЙ ФАЙЛОВ В КОМПЛЕКТЕ
-- ==========================================

CREATE TABLE document_file_roles (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID         REFERENCES organizations(id) ON DELETE CASCADE,
    display_name    VARCHAR(150) NOT NULL,
    sort_order      SMALLINT     NOT NULL DEFAULT 0,
    is_active       BOOLEAN      NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT uq_file_roles_org_name
        UNIQUE NULLS NOT DISTINCT (organization_id, display_name)
);

CREATE INDEX idx_file_roles_org_id ON document_file_roles(organization_id);

COMMENT ON TABLE  document_file_roles                 IS 'Справочник ролей файлов внутри комплекта договора; управляется тенантом';
COMMENT ON COLUMN document_file_roles.organization_id IS 'NULL — системная запись, видна всем тенантам; NOT NULL — запись конкретного тенанта';
COMMENT ON COLUMN document_file_roles.display_name    IS 'Человекочитаемое название: «Основной договор», «Смета», «ТЗ», «Матрица ответственности» …';
COMMENT ON COLUMN document_file_roles.sort_order      IS 'Порядок отображения в UI; меньше — выше';
COMMENT ON COLUMN document_file_roles.is_active       IS 'false — роль скрыта из выпадающих списков; существующие документы не затрагиваются';


-- ==========================================
-- COMPOSITE FK SUPPORT для справочников
-- ==========================================

ALTER TABLE document_contract_kinds
    ADD CONSTRAINT uq_contract_kinds_id_org UNIQUE (id, organization_id);

ALTER TABLE document_file_roles
    ADD CONSTRAINT uq_file_roles_id_org UNIQUE (id, organization_id);


-- ==========================================
-- TENANT ISOLATION для справочников
-- ==========================================

CREATE OR REPLACE FUNCTION check_document_kind_role_org() RETURNS trigger AS $$
BEGIN
    IF NEW.contract_kind_id IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM document_contract_kinds
            WHERE  id = NEW.contract_kind_id
              AND  (organization_id IS NULL OR organization_id = NEW.organization_id)
        ) THEN
            RAISE EXCEPTION
                'contract_kind_id % does not belong to organization % and is not a system record',
                NEW.contract_kind_id, NEW.organization_id;
        END IF;
    END IF;

    IF NEW.file_role_id IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM document_file_roles
            WHERE  id = NEW.file_role_id
              AND  (organization_id IS NULL OR organization_id = NEW.organization_id)
        ) THEN
            RAISE EXCEPTION
                'file_role_id % does not belong to organization % and is not a system record',
                NEW.file_role_id, NEW.organization_id;
        END IF;
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;


-- ==========================================
-- 4. ДОКУМЕНТЫ: тип договора, роль файла, комплект
-- ==========================================

ALTER TABLE documents
    ADD COLUMN contract_kind_id UUID REFERENCES document_contract_kinds(id) ON DELETE SET NULL,
    ADD COLUMN file_role_id     UUID REFERENCES document_file_roles(id)     ON DELETE SET NULL,
    ADD COLUMN bundle_id        UUID REFERENCES documents(id)               ON DELETE SET NULL;

ALTER TABLE documents
    ADD CONSTRAINT documents_artifact_kind_role_chk CHECK (
        parent_id IS NULL OR (
            contract_kind_id IS NULL AND
            file_role_id     IS NULL AND
            bundle_id        IS NULL
        )
    );

ALTER TABLE documents
    ADD CONSTRAINT documents_bundle_self_ref_chk CHECK (
        bundle_id IS DISTINCT FROM id
    );

CREATE INDEX idx_documents_contract_kind_id
    ON documents(contract_kind_id) WHERE contract_kind_id IS NOT NULL;
CREATE INDEX idx_documents_file_role_id
    ON documents(file_role_id)     WHERE file_role_id     IS NOT NULL;
CREATE INDEX idx_documents_bundle_id
    ON documents(bundle_id)        WHERE bundle_id        IS NOT NULL;

CREATE TRIGGER trg_document_kind_role_org
    BEFORE INSERT OR UPDATE ON documents
    FOR EACH ROW EXECUTE FUNCTION check_document_kind_role_org();

COMMENT ON COLUMN documents.contract_kind_id IS 'Тип договора из справочника document_contract_kinds; NULL для артефактов воркера';
COMMENT ON COLUMN documents.file_role_id     IS 'Роль файла в комплекте из справочника document_file_roles; NULL для артефактов воркера';
COMMENT ON COLUMN documents.bundle_id        IS 'Корневой документ комплекта; NULL — документ является корнем или не входит в комплект; артефакты воркера всегда NULL';


-- ==========================================
-- 5. КЛЮЧИ ИЗВЛЕЧЕНИЯ: человекочитаемое имя, активность, категория
-- ==========================================

ALTER TABLE extraction_keys
    ADD COLUMN display_name VARCHAR(150),
    ADD COLUMN is_active    BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN category     VARCHAR(50);

COMMENT ON COLUMN extraction_keys.display_name IS 'Человекочитаемое имя параметра для UI; NULL — fallback на source_query или key_name';
COMMENT ON COLUMN extraction_keys.is_active    IS 'false — ключ скрыт из UI, данные в document_extracted_data сохранены';
COMMENT ON COLUMN extraction_keys.category     IS 'Смысловая группа: commercial | volumetric | legal | other; NULL — без группировки';


-- ==========================================
-- 6. ПОЛЬЗОВАТЕЛИ: валидация роли, время входа
-- ==========================================

ALTER TABLE users
    ADD CONSTRAINT users_role_chk CHECK (role IN ('admin', 'member')),
    ADD COLUMN last_login_at TIMESTAMPTZ;

COMMENT ON COLUMN users.last_login_at IS 'Время последнего успешного входа; NULL если пользователь ещё не входил';


-- ==========================================
-- 7. ПРИГЛАШЕНИЯ ПОЛЬЗОВАТЕЛЕЙ
-- ==========================================

CREATE TABLE user_invitations (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email           VARCHAR(255) NOT NULL,
    role            VARCHAR(50)  NOT NULL DEFAULT 'member'
        CHECK (role IN ('admin', 'member')),
    invited_by      UUID         REFERENCES users(id) ON DELETE SET NULL,
    token_hash      VARCHAR(255) NOT NULL,
    expires_at      TIMESTAMPTZ  NOT NULL,
    accepted_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX uq_user_invitations_org_email_active
    ON user_invitations (organization_id, email)
    WHERE accepted_at IS NULL;

CREATE INDEX idx_user_invitations_organization_id ON user_invitations(organization_id);
CREATE INDEX idx_user_invitations_token_hash      ON user_invitations(token_hash);

COMMENT ON TABLE  user_invitations               IS 'Pending-приглашения пользователей в организацию; токен отправляется на email';
COMMENT ON COLUMN user_invitations.token_hash    IS 'Хэш одноразового токена из письма; сам токен не хранится';
COMMENT ON COLUMN user_invitations.expires_at    IS 'Срок действия приглашения; после истечения токен невалиден';
COMMENT ON COLUMN user_invitations.accepted_at   IS 'Время принятия; NULL — приглашение ещё не принято';


-- ==========================================
-- 8. ЛОГ СОБЫТИЙ ОБЪЕКТА
-- ==========================================

CREATE TABLE site_audit_log (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    site_id         UUID        NOT NULL REFERENCES construction_sites(id) ON DELETE CASCADE,
    actor_user_id   UUID        REFERENCES users(id) ON DELETE SET NULL,
    event_type      VARCHAR(50) NOT NULL,
    payload         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_site_audit_log_site_created
    ON site_audit_log(site_id, created_at DESC);

CREATE INDEX idx_site_audit_log_org_created
    ON site_audit_log(organization_id, created_at DESC);

COMMENT ON TABLE  site_audit_log                IS 'Лог событий объекта для вкладки «История» и блока «Последняя активность»';
COMMENT ON COLUMN site_audit_log.site_id        IS 'Объект, в котором произошло событие (всегда конечный объект, не родитель)';
COMMENT ON COLUMN site_audit_log.actor_user_id  IS 'Инициатор события; NULL для системных событий (watchdog, воркер)';
COMMENT ON COLUMN site_audit_log.event_type     IS 'Тип события; словарь расширяется приложением без миграции';
COMMENT ON COLUMN site_audit_log.payload        IS 'Контекст события; схема зависит от event_type';


-- ==========================================
-- 9. СЕССИИ СРАВНЕНИЯ
-- ==========================================

CREATE TABLE comparison_sessions (
    id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_by       UUID         REFERENCES users(id) ON DELETE SET NULL,
    name             VARCHAR(255),
    contract_kind_id UUID         REFERENCES document_contract_kinds(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_comparison_sessions_organization_id ON comparison_sessions(organization_id);

COMMENT ON TABLE  comparison_sessions                  IS 'Сохранённые сессии сравнения договоров; восстанавливаются по /compare?session=:id';
COMMENT ON COLUMN comparison_sessions.name             IS 'Человекочитаемое название сессии; NULL — автогенерация в UI';
COMMENT ON COLUMN comparison_sessions.contract_kind_id IS 'Тип договора, по которому выполнялось сравнение; NULL если тип был удалён';

CREATE TABLE comparison_session_documents (
    session_id      UUID     NOT NULL REFERENCES comparison_sessions(id) ON DELETE CASCADE,
    document_id     UUID     NOT NULL,
    organization_id UUID     NOT NULL,
    position        SMALLINT NOT NULL DEFAULT 0,
    PRIMARY KEY (session_id, document_id),
    CONSTRAINT fk_csd_doc_org
        FOREIGN KEY (document_id, organization_id)
        REFERENCES documents (id, organization_id)
        ON DELETE CASCADE
);

CREATE INDEX idx_csd_session_id ON comparison_session_documents(session_id);

COMMENT ON TABLE  comparison_session_documents                 IS 'Документы, участвующие в сессии сравнения';
COMMENT ON COLUMN comparison_session_documents.position        IS 'Порядок столбца в таблице сравнения; 0-based';
COMMENT ON COLUMN comparison_session_documents.organization_id IS 'Тенант; денормализован для composite FK-защиты (→ documents)';

CREATE TRIGGER trg_immut_org_contract_kinds
    BEFORE UPDATE OF organization_id ON document_contract_kinds
    FOR EACH ROW EXECUTE FUNCTION prevent_organization_id_change();

CREATE TRIGGER trg_immut_org_file_roles
    BEFORE UPDATE OF organization_id ON document_file_roles
    FOR EACH ROW EXECUTE FUNCTION prevent_organization_id_change();

CREATE TRIGGER trg_immut_org_comparison_sessions
    BEFORE UPDATE OF organization_id ON comparison_sessions
    FOR EACH ROW EXECUTE FUNCTION prevent_organization_id_change();

CREATE TRIGGER trg_immut_org_csd
    BEFORE UPDATE OF organization_id ON comparison_session_documents
    FOR EACH ROW EXECUTE FUNCTION prevent_organization_id_change();


-- ==========================================
-- 10. ТРИГГЕР last_activity_at
-- ==========================================

CREATE OR REPLACE FUNCTION propagate_site_activity() RETURNS trigger AS $$
DECLARE
    v_site_id UUID;
BEGIN
    IF TG_TABLE_NAME = 'documents' THEN
        v_site_id := NEW.site_id;
    ELSIF TG_TABLE_NAME = 'extraction_requests' THEN
        SELECT d.site_id INTO v_site_id
        FROM   documents d
        WHERE  d.id = NEW.document_id;
    END IF;

    IF v_site_id IS NULL THEN
        RETURN NEW;
    END IF;

    WITH RECURSIVE ancestors AS (
        SELECT id, parent_id
        FROM   construction_sites
        WHERE  id = v_site_id
        UNION ALL
        SELECT cs.id, cs.parent_id
        FROM   construction_sites cs
        JOIN   ancestors a ON cs.id = a.parent_id
    )
    UPDATE construction_sites
    SET    last_activity_at = now(),
           updated_at       = now()
    WHERE  id IN (SELECT id FROM ancestors);

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_documents_propagate_activity
    AFTER INSERT ON documents
    FOR EACH ROW EXECUTE FUNCTION propagate_site_activity();

CREATE TRIGGER trg_extraction_requests_propagate_activity
    AFTER INSERT OR UPDATE OF status ON extraction_requests
    FOR EACH ROW EXECUTE FUNCTION propagate_site_activity();


-- ==========================================
-- 11. VIEW v_site_status
-- ==========================================

CREATE VIEW v_site_status AS
WITH RECURSIVE

site_tree AS (
    SELECT id, organization_id, id AS root_id
    FROM   construction_sites
    UNION ALL
    SELECT cs.id, cs.organization_id, st.root_id
    FROM   construction_sites cs
    JOIN   site_tree st ON cs.parent_id = st.id
),

leaf_status AS (
    SELECT
        s.id AS site_id,
        s.organization_id,
        CASE
            WHEN NOT EXISTS (
                SELECT 1 FROM documents d
                WHERE  d.site_id = s.id AND d.parent_id IS NULL
            ) THEN 'empty'

            WHEN EXISTS (
                SELECT 1
                FROM   document_tasks dt
                JOIN   documents d ON d.id = dt.document_id
                WHERE  d.site_id = s.id
                  AND  dt.status = 'failed'
                  AND  dt.extraction_request_id IS NULL
            ) OR EXISTS (
                SELECT 1
                FROM   extraction_requests er
                JOIN   documents d ON d.id = er.document_id
                WHERE  d.site_id = s.id
                  AND  er.status = 'failed'
            ) THEN 'attention'

            WHEN EXISTS (
                SELECT 1
                FROM   document_tasks dt
                JOIN   documents d ON d.id = dt.document_id
                WHERE  d.site_id = s.id
                  AND  dt.status IN ('pending', 'processing')
            ) OR EXISTS (
                SELECT 1
                FROM   extraction_requests er
                JOIN   documents d ON d.id = er.document_id
                WHERE  d.site_id = s.id
                  AND  er.status IN ('pending', 'running')
            ) THEN 'processing'

            ELSE 'ready'
        END AS status
    FROM construction_sites s
    WHERE NOT EXISTS (
        SELECT 1 FROM construction_sites c WHERE c.parent_id = s.id
    )
)

SELECT
    st.root_id                                                   AS site_id,
    (SELECT cs.organization_id
     FROM   construction_sites cs
     WHERE  cs.id = st.root_id)                                  AS organization_id,

    CASE
        WHEN COUNT(*) FILTER (WHERE ls.status = 'attention')  > 0 THEN 'attention'
        WHEN COUNT(*) FILTER (WHERE ls.status = 'processing') > 0 THEN 'processing'
        WHEN COUNT(*) FILTER (WHERE ls.status = 'empty') = COUNT(*) THEN 'empty'
        ELSE 'ready'
    END                                                          AS status,

    COUNT(*) FILTER (WHERE ls.status = 'ready')                  AS children_ready,
    COUNT(*) FILTER (WHERE ls.status = 'processing')             AS children_processing,
    COUNT(*) FILTER (WHERE ls.status = 'attention')              AS children_attention,
    COUNT(*) FILTER (WHERE ls.status = 'empty')                  AS children_empty

FROM      site_tree  st
JOIN      leaf_status ls ON ls.site_id = st.id
GROUP BY  st.root_id;

COMMENT ON VIEW v_site_status IS
    'Рекурсивный агрегат статуса объекта по всему поддереву. '
    'Одна строка на каждый объект (корневой, промежуточный, конечный). '
    'children_* — количество листьев поддерева в каждом статусе. '
    'Для конечного объекта ровно один счётчик равен 1.';
