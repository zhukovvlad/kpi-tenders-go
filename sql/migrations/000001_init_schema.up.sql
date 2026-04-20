-- Расширение для работы с векторными эмбеддингами (используется в RAG-поиске)
CREATE EXTENSION IF NOT EXISTS vector;

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

COMMENT ON TABLE organizations IS 'Организации — изолированные тенанты системы';
COMMENT ON COLUMN organizations.inn IS 'ИНН: 10 цифр для ЮЛ, 12 для ИП; NULL если не указан';
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

COMMENT ON TABLE users IS 'Пользователи, привязанные к организации';
COMMENT ON COLUMN users.role IS 'Роль пользователя: admin — полный доступ к тенанту, member — работа с документами';
COMMENT ON COLUMN users.is_active IS 'false — пользователь деактивирован, вход заблокирован';

-- ==========================================
-- ОБЪЕКТЫ СТРОИТЕЛЬСТВА
-- ==========================================

CREATE TABLE construction_sites (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    parent_id       UUID         REFERENCES construction_sites(id) ON DELETE SET NULL,
    name            VARCHAR(255) NOT NULL,
    status          VARCHAR(50)  NOT NULL DEFAULT 'active',
    created_by      UUID         REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE construction_sites IS 'Объекты строительства (ЖК, очереди, корпуса) — иерархическая структура тенанта';
COMMENT ON COLUMN construction_sites.parent_id IS 'ID родительского объекта; NULL для корневых объектов (например, сам ЖК)';
COMMENT ON COLUMN construction_sites.status IS 'Статус жизненного цикла: active | completed | archived';
COMMENT ON COLUMN construction_sites.created_by IS 'Пользователь, создавший объект; NULL если пользователь удалён';

-- ==========================================
-- ДОКУМЕНТЫ
-- ==========================================

CREATE TABLE documents (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID         NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    site_id         UUID         REFERENCES construction_sites(id) ON DELETE SET NULL,
    uploaded_by     UUID         NOT NULL REFERENCES users(id),
    parent_id       UUID         REFERENCES documents(id) ON DELETE SET NULL,
    file_name       VARCHAR(255) NOT NULL,
    storage_path    TEXT         NOT NULL,
    mime_type       VARCHAR(100),
    file_size_bytes BIGINT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE documents IS 'Метаданные загруженных файлов; физический файл хранится в MinIO';
COMMENT ON COLUMN documents.site_id IS 'Объект строительства, к которому относится документ; NULL — документ без привязки';
COMMENT ON COLUMN documents.parent_id IS 'Исходный документ, если текущий получен в результате обработки (конвертация, анонимизация)';
COMMENT ON COLUMN documents.storage_path IS 'Путь к файлу в MinIO: bucket/prefix/uuid.ext';
COMMENT ON COLUMN documents.mime_type IS 'MIME-тип файла, определяется при загрузке; NULL если не определён';
COMMENT ON COLUMN documents.file_size_bytes IS 'Размер файла в байтах; NULL если не известен на момент создания записи';

-- ==========================================
-- ЗАДАЧИ AI-ВОРКЕРА
-- ==========================================

CREATE TABLE document_tasks (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     UUID         NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    module_name     TEXT         NOT NULL,
    status          VARCHAR(50)  NOT NULL DEFAULT 'pending',
    celery_task_id  VARCHAR(255),
    result_payload  JSONB        NOT NULL DEFAULT '{}'::jsonb,
    error_message   TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

COMMENT ON TABLE document_tasks IS 'Задачи AI-воркера на Python для обработки документов';
COMMENT ON COLUMN document_tasks.module_name IS 'Маршрутизатор воркера: определяет логику обработки (например: anonymize, parse_estimate, convert). Набор значений расширяется по мере добавления модулей';
COMMENT ON COLUMN document_tasks.status IS 'Статус выполнения: pending | processing | completed | failed';
COMMENT ON COLUMN document_tasks.celery_task_id IS 'UUID задачи в Celery; заполняется воркером при взятии задачи в работу';
COMMENT ON COLUMN document_tasks.result_payload IS 'Структурированный результат обработки; схема зависит от module_name';
COMMENT ON COLUMN document_tasks.error_message IS 'Описание ошибки при status = failed; NULL в остальных случаях';

-- ==========================================
-- ИНДЕКСЫ
-- ==========================================

CREATE INDEX idx_users_organization_id         ON users(organization_id);
CREATE INDEX idx_sites_organization_id         ON construction_sites(organization_id);
CREATE INDEX idx_sites_parent_id               ON construction_sites(parent_id);
CREATE INDEX idx_documents_organization_id     ON documents(organization_id);
CREATE INDEX idx_documents_site_id             ON documents(site_id);
CREATE INDEX idx_documents_parent_id           ON documents(parent_id);
CREATE INDEX idx_tasks_document_id             ON document_tasks(document_id);
CREATE INDEX idx_tasks_status                  ON document_tasks(status);
