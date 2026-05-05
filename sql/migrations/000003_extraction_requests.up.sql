-- ==========================================
-- ЗАПРОСЫ ЭКСТРАКЦИИ
-- ==========================================
-- Пользовательский запрос на извлечение данных из документа: набор вопросов
-- + флаг анонимизации. Один запрос порождает цепочку document_tasks
-- (resolve_keys + extract); при необходимости предварительно запускаются
-- singleton-таски convert/anonymize по документу.
--
-- Несколько extraction_requests на один документ допустимы и независимы:
-- каждый запрос имеет свой набор вопросов, свой resolved_schema и свой
-- результат. Артефакты документа (markdown / anonymized) при этом
-- остаются singleton — повторно не создаются.

CREATE TABLE extraction_requests (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id     UUID         NOT NULL,
    organization_id UUID         NOT NULL,
    questions       JSONB        NOT NULL,
    anonymize       BOOLEAN      NOT NULL DEFAULT TRUE,
    status          VARCHAR(20)  NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    resolved_schema JSONB,
    error_message   TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    -- composite FK: документ должен принадлежать той же организации, что и запрос
    CONSTRAINT fk_extraction_requests_doc_org
        FOREIGN KEY (document_id, organization_id)
        REFERENCES documents (id, organization_id)
        ON DELETE CASCADE,
    CONSTRAINT extraction_requests_questions_chk
        CHECK (jsonb_typeof(questions) = 'array' AND jsonb_array_length(questions) > 0)
);

CREATE INDEX idx_extraction_requests_document_id
    ON extraction_requests(document_id);

-- Быстрый поиск незавершённых запросов на документ — используется при
-- progressRequest после завершения convert/anonymize, чтобы продвинуть все
-- ожидающие запросы дальше.
CREATE INDEX idx_extraction_requests_doc_pending
    ON extraction_requests(document_id)
    WHERE status IN ('pending', 'running');

CREATE TRIGGER trg_immut_org_extraction_requests
    BEFORE UPDATE OF organization_id ON extraction_requests
    FOR EACH ROW EXECUTE FUNCTION prevent_organization_id_change();

COMMENT ON TABLE  extraction_requests                IS 'Пользовательский запрос на извлечение данных из документа (один комплект вопросов)';
COMMENT ON COLUMN extraction_requests.questions      IS 'Массив строк-вопросов на естественном языке (jsonb array)';
COMMENT ON COLUMN extraction_requests.anonymize      IS 'true — extract читает анонимизированный артефакт; false — читает markdown-артефакт';
COMMENT ON COLUMN extraction_requests.status         IS 'pending — ждёт prereq-задач; running — resolve_keys/extract в работе; completed — ответы готовы; failed — ошибка';
COMMENT ON COLUMN extraction_requests.resolved_schema IS 'JSON-схема ключей, возвращённая resolve_keys; используется для выборки ответов из document_extracted_data';

-- ==========================================
-- DOCUMENT_TASKS: привязка к extraction_request
-- ==========================================
-- convert / anonymize — singleton на документ (extraction_request_id IS NULL).
-- resolve_keys / extract — один на extraction_request (extraction_request_id NOT NULL).
-- Старая UNIQUE-константа (document_id, module_name) ломается на параллельных
-- extraction_requests, поэтому заменена на два частичных индекса.

ALTER TABLE document_tasks
    ADD COLUMN extraction_request_id UUID
        REFERENCES extraction_requests(id) ON DELETE CASCADE;

ALTER TABLE document_tasks
    DROP CONSTRAINT uq_document_tasks_document_module;

-- singleton-таски документа: convert и anonymize не привязаны к запросу.
CREATE UNIQUE INDEX uq_document_tasks_doc_singleton
    ON document_tasks (document_id, module_name)
    WHERE module_name IN ('convert', 'anonymize')
      AND extraction_request_id IS NULL;

-- per-request таски: один resolve_keys и один extract на extraction_request.
CREATE UNIQUE INDEX uq_document_tasks_request_module
    ON document_tasks (extraction_request_id, module_name)
    WHERE extraction_request_id IS NOT NULL
      AND module_name IN ('resolve_keys', 'extract');

-- Инвариант на стороне БД: convert/anonymize всегда без extraction_request_id,
-- resolve_keys/extract — всегда с ним.
ALTER TABLE document_tasks
    ADD CONSTRAINT document_tasks_module_request_chk CHECK (
        (module_name IN ('convert', 'anonymize') AND extraction_request_id IS NULL) OR
        (module_name IN ('resolve_keys', 'extract') AND extraction_request_id IS NOT NULL) OR
        (module_name NOT IN ('convert', 'anonymize', 'resolve_keys', 'extract'))
    );

CREATE INDEX idx_document_tasks_extraction_request_id
    ON document_tasks(extraction_request_id)
    WHERE extraction_request_id IS NOT NULL;

COMMENT ON COLUMN document_tasks.extraction_request_id IS
    'Запрос экстракции, к которому относится таска. NULL для convert/anonymize (singleton документа); NOT NULL для resolve_keys/extract.';
