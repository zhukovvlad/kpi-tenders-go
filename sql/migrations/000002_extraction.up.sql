-- ==========================================
-- COMPOSITE FK SUPPORT
-- ==========================================
-- PostgreSQL requires an explicit UNIQUE CONSTRAINT on (id, organization_id) so that
-- document_extracted_data can declare a composite FK for tenant isolation.
-- id is already a PK (unique), so this constraint adds no data guarantee beyond what
-- the PK provides — only enables the FK reference below.
ALTER TABLE documents
    ADD CONSTRAINT uq_documents_id_org UNIQUE (id, organization_id);

-- ==========================================
-- КЛЮЧИ ИЗВЛЕЧЕНИЯ
-- ==========================================
-- Справочник семантических ключей, по которым выполняется извлечение данных из документов.
-- organization_id IS NULL означает системный (общий) ключ, видимый всем тенантам.
-- UNIQUE NULLS NOT DISTINCT (PostgreSQL 15+): два NULL считаются равными,
-- поэтому не может быть двух системных ключей с одним key_name.

CREATE TABLE extraction_keys (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        REFERENCES organizations(id) ON DELETE CASCADE,
    key_name        VARCHAR(50) NOT NULL,
    source_query    TEXT        NOT NULL,
    data_type       VARCHAR(20) NOT NULL DEFAULT 'string',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_extraction_keys_org_name
        UNIQUE NULLS NOT DISTINCT (organization_id, key_name)
);

CREATE INDEX idx_extraction_keys_org_id ON extraction_keys(organization_id);

COMMENT ON TABLE  extraction_keys                    IS 'Ключи для семантического извлечения данных из документов';
COMMENT ON COLUMN extraction_keys.organization_id    IS 'NULL — системный ключ (общий для всех тенантов); NOT NULL — ключ конкретного тенанта';
COMMENT ON COLUMN extraction_keys.key_name           IS 'Машинное имя ключа (латиница + подчёркивание, макс. 50 симв.)';
COMMENT ON COLUMN extraction_keys.source_query       IS 'Исходный вопрос на естественном языке, на основе которого создан ключ';
COMMENT ON COLUMN extraction_keys.data_type          IS 'Тип значения: string | number | date | boolean';

-- ==========================================
-- ИЗВЛЕЧЁННЫЕ ДАННЫЕ ИЗ ДОКУМЕНТОВ
-- ==========================================
-- Хранит результаты работы Python-воркера модуля extract.
-- Составной FK (document_id, organization_id) → documents(id, organization_id)
-- гарантирует на уровне БД, что данные принадлежат документу того же тенанта.

CREATE TABLE document_extracted_data (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID        NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    document_id     UUID        NOT NULL,
    key_id          UUID        NOT NULL REFERENCES extraction_keys(id) ON DELETE CASCADE,
    extracted_value TEXT,
    CONSTRAINT uq_extracted_data_doc_key
        UNIQUE (document_id, key_id),
    CONSTRAINT fk_extracted_data_doc_org
        FOREIGN KEY (document_id, organization_id)
        REFERENCES documents (id, organization_id)
        ON DELETE CASCADE
);

CREATE INDEX idx_extracted_data_document_id    ON document_extracted_data(document_id);
CREATE INDEX idx_extracted_data_organization_id ON document_extracted_data(organization_id);

COMMENT ON TABLE  document_extracted_data                  IS 'Извлечённые значения ключей для конкретного документа';
COMMENT ON COLUMN document_extracted_data.organization_id  IS 'Тенант; денормализован для composite FK-защиты (→ documents)';
COMMENT ON COLUMN document_extracted_data.document_id      IS 'Документ, из которого извлечены данные';
COMMENT ON COLUMN document_extracted_data.key_id           IS 'Ключ извлечения';
COMMENT ON COLUMN document_extracted_data.extracted_value  IS 'Извлечённое значение; NULL если воркер не смог извлечь';
