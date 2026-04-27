-- gen_random_uuid() встроен в PostgreSQL 13+, но pgcrypto обеспечивает
-- совместимость и предоставляет дополнительные криптографические функции.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ==========================================
-- ОРГАНИЗАЦИИ
-- ==========================================

CREATE TABLE organizations (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) NOT NULL,
    inn         VARCHAR(12)  UNIQUE,
    settings    JSONB        NOT NULL DEFAULT '{}'::jsonb,
    is_active   BOOLEAN      NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE  organizations          IS 'Организации — изолированные тенанты системы';
COMMENT ON COLUMN organizations.inn      IS 'ИНН: 10 цифр для ЮЛ, 12 для ИП; NULL если не указан';
COMMENT ON COLUMN organizations.settings IS 'Произвольные настройки тенанта (тема, лимиты, флаги)';
COMMENT ON COLUMN organizations.is_active IS 'false — организация деактивирована, вход для всех её пользователей заблокирован';

-- ==========================================
-- ПОЛЬЗОВАТЕЛИ
-- ==========================================

CREATE TABLE users (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email           VARCHAR(255) NOT NULL UNIQUE,
    password_hash   VARCHAR(255) NOT NULL,
    full_name       VARCHAR(255) NOT NULL,
    role            VARCHAR(50)  NOT NULL DEFAULT 'member',
    is_active       BOOLEAN      NOT NULL DEFAULT true,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE  users           IS 'Пользователи, привязанные к организации';
COMMENT ON COLUMN users.role      IS 'Роль пользователя: admin — полный доступ к тенанту, member — работа с документами';
COMMENT ON COLUMN users.is_active IS 'false — пользователь деактивирован, вход заблокирован';

-- ==========================================
-- ОБЪЕКТЫ СТРОИТЕЛЬСТВА
-- ==========================================

CREATE TABLE construction_sites (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    parent_id       UUID         REFERENCES construction_sites(id) ON DELETE SET NULL,
    name            VARCHAR(255) NOT NULL,
    status          VARCHAR(50)  NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'completed', 'archived')),
    created_by      UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE  construction_sites            IS 'Объекты строительства (ЖК, очереди, корпуса) — иерархическая структура тенанта';
COMMENT ON COLUMN construction_sites.parent_id  IS 'ID родительского объекта; NULL для корневых объектов (например, сам ЖК)';
COMMENT ON COLUMN construction_sites.status     IS 'Статус жизненного цикла: active | completed | archived';
COMMENT ON COLUMN construction_sites.created_by IS 'Пользователь, создавший объект; NULL если пользователь удалён';

-- ==========================================
-- ДОКУМЕНТЫ
-- ==========================================
-- Хранит метаданные как оригинальных документов (загруженных пользователем),
-- так и артефактов — файлов, порождённых AI-воркером (конвертация, анонимизация).
-- Физические файлы хранятся в MinIO; storage_path — ключ для доступа к ним.

CREATE TABLE documents (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    site_id         UUID         REFERENCES construction_sites(id) ON DELETE SET NULL,
    uploaded_by     UUID         NOT NULL REFERENCES users(id),
    -- parent_id: NULL — оригинал, загруженный пользователем;
    --            ≠ NULL — артефакт, produced by AI-воркером из parent_id документа.
    -- ON DELETE CASCADE: артефакт без источника не имеет смысла.
    parent_id       UUID         REFERENCES documents(id) ON DELETE CASCADE,
    file_name       VARCHAR(255) NOT NULL,
    storage_path    TEXT         NOT NULL,
    mime_type       VARCHAR(100),
    file_size_bytes BIGINT,
    -- artifact_kind: NULL — оригинал; непустая строка — вид артефакта.
    -- Вместе с parent_id формирует UNIQUE: один вид артефакта на один исходный документ.
    artifact_kind   VARCHAR(50),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- Инвариант: оригинал (parent_id IS NULL) обязан иметь artifact_kind IS NULL;
    -- артефакт (parent_id IS NOT NULL) обязан иметь artifact_kind IS NOT NULL.
    CONSTRAINT documents_parent_artifact_kind_chk CHECK (
        (parent_id IS NULL AND artifact_kind IS NULL) OR
        (parent_id IS NOT NULL AND artifact_kind IS NOT NULL AND btrim(artifact_kind) <> '')
    ),
    CONSTRAINT uq_documents_id_organization UNIQUE (id, organization_id)
);

COMMENT ON TABLE  documents                    IS 'Метаданные загруженных файлов и AI-артефактов; физические файлы — в MinIO';
COMMENT ON COLUMN documents.site_id            IS 'Объект строительства документа; NULL — документ без привязки к объекту';
COMMENT ON COLUMN documents.parent_id          IS 'Исходный документ, если запись является артефактом воркера; NULL для оригиналов';
COMMENT ON COLUMN documents.storage_path       IS 'Путь к файлу в MinIO: bucket/prefix/uuid.ext';
COMMENT ON COLUMN documents.mime_type          IS 'MIME-тип файла, определяется при загрузке; NULL если не определён';
COMMENT ON COLUMN documents.file_size_bytes    IS 'Размер файла в байтах; NULL если не известен на момент создания записи';
COMMENT ON COLUMN documents.artifact_kind      IS 'Тип артефакта: NULL — загружен пользователем; convert_md — результат конвертации в Markdown; anonymize_doc — анонимизированный документ; anonymize_entities — карта сущностей анонимизации';

-- ==========================================
-- КЛЮЧИ ИЗВЛЕЧЕНИЯ ДАННЫХ
-- ==========================================

CREATE TABLE extraction_keys (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key_name        TEXT         NOT NULL CHECK (btrim(key_name) <> ''),
    source_query    TEXT         NOT NULL CHECK (btrim(source_query) <> ''),
    description     TEXT,
    data_type       TEXT         NOT NULL DEFAULT 'string'
        CHECK (data_type IN ('string', 'number', 'integer', 'boolean', 'date', 'json')),
    is_required     BOOLEAN      NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT uq_extraction_keys_id_organization UNIQUE (id, organization_id),
    CONSTRAINT uq_extraction_keys_org_key UNIQUE (organization_id, key_name)
);

COMMENT ON TABLE  extraction_keys                 IS 'Нормализованные ключи извлечения данных из документов';
COMMENT ON COLUMN extraction_keys.organization_id IS 'Организация-владелец ключа';
COMMENT ON COLUMN extraction_keys.key_name        IS 'Техническое имя ключа, например advance_payment_percent';
COMMENT ON COLUMN extraction_keys.source_query    IS 'Исходный пользовательский вопрос, из которого был получен ключ';
COMMENT ON COLUMN extraction_keys.description     IS 'Краткое описание смысла ключа для воркера извлечения';
COMMENT ON COLUMN extraction_keys.data_type       IS 'Ожидаемый тип значения: string | number | integer | boolean | date | json';
COMMENT ON COLUMN extraction_keys.is_required     IS 'true — отсутствие значения должно считаться важной проблемой качества извлечения';

CREATE TABLE document_extracted_data (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    document_id     UUID         NOT NULL,
    key_id          UUID         NOT NULL,
    extracted_value JSONB        NOT NULL,
    confidence      DOUBLE PRECISION CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT fk_extracted_data_document_org
        FOREIGN KEY (document_id, organization_id)
        REFERENCES documents(id, organization_id)
        ON DELETE CASCADE,
    CONSTRAINT fk_extracted_data_key_org
        FOREIGN KEY (key_id, organization_id)
        REFERENCES extraction_keys(id, organization_id)
        ON DELETE CASCADE,
    CONSTRAINT uq_document_extracted_data_org_document_key UNIQUE (organization_id, document_id, key_id)
);

COMMENT ON TABLE  document_extracted_data                 IS 'Значения, извлечённые воркером из документа по нормализованным ключам';
COMMENT ON COLUMN document_extracted_data.organization_id IS 'Организация для tenant isolation';
COMMENT ON COLUMN document_extracted_data.document_id     IS 'Документ, из которого извлечено значение';
COMMENT ON COLUMN document_extracted_data.key_id          IS 'Нормализованный ключ извлечения';
COMMENT ON COLUMN document_extracted_data.extracted_value IS 'Извлечённое значение в JSONB, чтобы сохранить исходный тип';
COMMENT ON COLUMN document_extracted_data.confidence      IS 'Уверенность воркера от 0 до 1; NULL если не передана';

-- ==========================================
-- ЗАДАЧИ AI-ВОРКЕРА
-- ==========================================

CREATE TABLE document_tasks (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     UUID         NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    module_name     TEXT         NOT NULL,
    status          VARCHAR(50)  NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    celery_task_id  VARCHAR(255),
    result_payload  JSONB        NOT NULL DEFAULT '{}'::jsonb,
    error_message   TEXT,
    -- retry_count: сколько раз watchdog уже перезапускал задачу.
    -- Инкрементируется при каждом сбросе в 'pending'; не сбрасывается при completed/failed.
    retry_count     INT          NOT NULL DEFAULT 0,
    -- input_storage_path: путь к файлу, который воркер должен обработать.
    -- Для 'convert':   storage_path исходного документа (PDF).
    -- Для 'anonymize': storage_path артефакта convert_md (Markdown).
    -- Хранится явно, чтобы watchdog мог переотправить задачу с правильным путём,
    -- не вычисляя его через JOIN и не смешивая пути разных модулей.
    input_storage_path TEXT      NOT NULL
                        CHECK (btrim(input_storage_path) <> ''),
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    CONSTRAINT uq_document_tasks_document_module UNIQUE (document_id, module_name)
);

COMMENT ON TABLE  document_tasks                      IS 'Задачи AI-воркера на Python для обработки документов';
COMMENT ON COLUMN document_tasks.module_name          IS 'Маршрутизатор воркера: определяет логику обработки (convert, anonymize, parse_invoice, extract и др.)';
COMMENT ON COLUMN document_tasks.status               IS 'Статус выполнения: pending | processing | completed | failed';
COMMENT ON COLUMN document_tasks.celery_task_id       IS 'UUID задачи в Celery; заполняется воркером при взятии задачи в работу';
COMMENT ON COLUMN document_tasks.result_payload       IS 'Структурированный результат обработки; схема зависит от module_name';
COMMENT ON COLUMN document_tasks.error_message        IS 'Описание ошибки при status = failed; NULL в остальных случаях';
COMMENT ON COLUMN document_tasks.retry_count          IS 'Число перезапусков watchdog-ом; при превышении WatchdogMaxRetries задача переводится в failed';
COMMENT ON COLUMN document_tasks.input_storage_path   IS 'Путь к входному файлу воркера в MinIO; определяется модулем и фиксируется при создании задачи';

-- ==========================================
-- ИНДЕКСЫ
-- ==========================================

-- Базовые FK-индексы для joins и фильтрации по тенанту
CREATE INDEX idx_users_organization_id     ON users(organization_id);
CREATE INDEX idx_sites_organization_id     ON construction_sites(organization_id);
CREATE INDEX idx_sites_parent_id           ON construction_sites(parent_id);
CREATE INDEX idx_documents_organization_id ON documents(organization_id);
CREATE INDEX idx_documents_site_id         ON documents(site_id);
CREATE INDEX idx_documents_parent_id       ON documents(parent_id);
CREATE INDEX idx_extraction_keys_org_created_at
    ON extraction_keys(organization_id, created_at);
CREATE INDEX idx_extraction_keys_org_norm_source_query
    ON extraction_keys(organization_id, lower(btrim(source_query)));
CREATE INDEX idx_extracted_data_key_org    ON document_extracted_data(key_id, organization_id);
CREATE INDEX idx_tasks_document_id         ON document_tasks(document_id);
CREATE INDEX idx_tasks_status              ON document_tasks(status);

-- Watchdog: быстрый поиск зависших задач по времени обновления.
-- Частичный индекс покрывает только 'pending' и 'processing' — единственные
-- статусы, которые сторожевой таймер проверяет на зависание.
CREATE INDEX idx_document_tasks_stale
    ON document_tasks (updated_at ASC)
    WHERE status IN ('pending', 'processing');

-- Быстрый листинг только корневых документов (загруженных пользователем),
-- исключая артефакты воркера из общего списка.
CREATE INDEX idx_documents_root
    ON documents(organization_id, created_at DESC)
    WHERE parent_id IS NULL;

-- Идемпотентность артефактов: гарантирует один артефакт каждого вида
-- на один исходный документ (уникальная пара parent_id + artifact_kind).
-- Позволяет ON CONFLICT DO UPDATE обновлять существующий артефакт при повторном callback.
CREATE UNIQUE INDEX idx_documents_artifact_kind
    ON documents (parent_id, artifact_kind)
    WHERE parent_id IS NOT NULL;

-- ==========================================
-- TENANT ISOLATION: триггеры same-org проверок
-- ==========================================
-- PostgreSQL не поддерживает composite FK с nullable колонками,
-- поэтому same-org checks реализованы через BEFORE INSERT/UPDATE триггеры.

CREATE OR REPLACE FUNCTION check_site_org_isolation() RETURNS trigger AS $$
BEGIN
    -- parent_id: самоссылка и цикличность запрещены
    IF NEW.parent_id IS NOT NULL THEN
        IF NEW.parent_id = NEW.id THEN
            RAISE EXCEPTION 'parent_id cannot reference the same construction site %', NEW.id;
        END IF;
        -- родительский объект должен принадлежать той же организации
        IF NOT EXISTS (
            SELECT 1 FROM construction_sites
            WHERE id = NEW.parent_id AND organization_id = NEW.organization_id
        ) THEN
            RAISE EXCEPTION 'parent_id % does not belong to organization %',
                NEW.parent_id, NEW.organization_id;
        END IF;
        -- обнаружение цикла через обход предков
        IF EXISTS (
            WITH RECURSIVE ancestors AS (
                SELECT id, parent_id
                FROM construction_sites
                WHERE id = NEW.parent_id
                UNION ALL
                SELECT cs.id, cs.parent_id
                FROM construction_sites cs
                JOIN ancestors a ON cs.id = a.parent_id
            )
            SELECT 1 FROM ancestors WHERE id = NEW.id
        ) THEN
            RAISE EXCEPTION 'parent_id % would create a construction site cycle', NEW.parent_id;
        END IF;
    END IF;
    -- created_by: пользователь должен принадлежать той же организации
    IF NEW.created_by IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM users
            WHERE id = NEW.created_by AND organization_id = NEW.organization_id
        ) THEN
            RAISE EXCEPTION 'created_by % does not belong to organization %',
                NEW.created_by, NEW.organization_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_site_org_isolation
    BEFORE INSERT OR UPDATE ON construction_sites
    FOR EACH ROW EXECUTE FUNCTION check_site_org_isolation();

CREATE OR REPLACE FUNCTION check_document_org_isolation() RETURNS trigger AS $$
BEGIN
    -- uploaded_by: пользователь должен принадлежать той же организации
    IF NOT EXISTS (
        SELECT 1 FROM users
        WHERE id = NEW.uploaded_by AND organization_id = NEW.organization_id
    ) THEN
        RAISE EXCEPTION 'uploaded_by % does not belong to organization %',
            NEW.uploaded_by, NEW.organization_id;
    END IF;
    -- site_id: объект строительства должен принадлежать той же организации
    IF NEW.site_id IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM construction_sites
            WHERE id = NEW.site_id AND organization_id = NEW.organization_id
        ) THEN
            RAISE EXCEPTION 'site_id % does not belong to organization %',
                NEW.site_id, NEW.organization_id;
        END IF;
    END IF;
    -- parent_id: родительский документ должен принадлежать той же организации
    IF NEW.parent_id IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM documents
            WHERE id = NEW.parent_id AND organization_id = NEW.organization_id
        ) THEN
            RAISE EXCEPTION 'parent_id % does not belong to organization %',
                NEW.parent_id, NEW.organization_id;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_document_org_isolation
    BEFORE INSERT OR UPDATE ON documents
    FOR EACH ROW EXECUTE FUNCTION check_document_org_isolation();

-- ==========================================
-- TENANT ISOLATION: запрет смены organization_id
-- ==========================================
CREATE OR REPLACE FUNCTION prevent_organization_id_change() RETURNS trigger AS $$
BEGIN
    IF OLD.organization_id IS DISTINCT FROM NEW.organization_id THEN
        RAISE EXCEPTION 'organization_id cannot be changed for % %',
            TG_TABLE_NAME, OLD.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_users_prevent_org_change
    BEFORE UPDATE OF organization_id ON users
    FOR EACH ROW EXECUTE FUNCTION prevent_organization_id_change();

CREATE TRIGGER trg_sites_prevent_org_change
    BEFORE UPDATE OF organization_id ON construction_sites
    FOR EACH ROW EXECUTE FUNCTION prevent_organization_id_change();

CREATE TRIGGER trg_documents_prevent_org_change
    BEFORE UPDATE OF organization_id ON documents
    FOR EACH ROW EXECUTE FUNCTION prevent_organization_id_change();

CREATE TRIGGER trg_extraction_keys_prevent_org_change
    BEFORE UPDATE OF organization_id ON extraction_keys
    FOR EACH ROW EXECUTE FUNCTION prevent_organization_id_change();

CREATE TRIGGER trg_document_extracted_data_prevent_org_change
    BEFORE UPDATE OF organization_id ON document_extracted_data
    FOR EACH ROW EXECUTE FUNCTION prevent_organization_id_change();
