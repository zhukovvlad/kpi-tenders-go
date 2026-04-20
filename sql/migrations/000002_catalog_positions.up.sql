-- 000002_catalog_positions.up.sql
-- Stores extracted tender line-items with vector embeddings for RAG search.

CREATE TABLE catalog_positions (
    id           UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    document_id  UUID        NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    title        TEXT        NOT NULL,
    -- embedding dimension is not fixed here; set it once the AI model is finalised
    -- (e.g. vector(1536) for OpenAI text-embedding-ada-002).
    embedding    vector,
    -- Arbitrary key-value specs extracted from the tender document.
    parameters   JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_catalog_positions_document_id  ON catalog_positions(document_id);
-- GIN index for efficient JSONB containment queries (@>).
CREATE INDEX idx_catalog_positions_parameters   ON catalog_positions USING gin(parameters);

-- After schema stabilises, add an IVFFlat index for cosine similarity:
-- CREATE INDEX idx_catalog_positions_embedding
--   ON catalog_positions USING ivfflat (embedding vector_cosine_ops)
--   WITH (lists = 100);
