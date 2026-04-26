-- Добавить поле artifact_kind
ALTER TABLE documents
    ADD COLUMN artifact_kind varchar(50) NULL;

COMMENT ON COLUMN documents.artifact_kind IS
    'Тип артефакта: NULL — загружен пользователем, '
    'convert_md — результат конвертации в Markdown, '
    'anonymize_doc — анонимизированный документ, '
    'anonymize_entities — карта сущностей анонимизации';

-- Исправить FK: SET NULL → CASCADE (артефакт без источника бессмысленен)
ALTER TABLE documents
    DROP CONSTRAINT documents_parent_id_fkey,
    ADD CONSTRAINT documents_parent_id_fkey
        FOREIGN KEY (parent_id) REFERENCES documents(id) ON DELETE CASCADE;

-- Индекс для быстрого листинга только корневых документов
CREATE INDEX idx_documents_root
    ON documents(organization_id, created_at DESC)
    WHERE parent_id IS NULL;
