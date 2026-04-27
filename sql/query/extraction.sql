-- name: ListExtractionKeysByOrg :many
-- Returns all keys visible to the given tenant: org-specific keys AND system
-- keys (organization_id IS NULL) shared across all tenants.
SELECT * FROM extraction_keys
WHERE organization_id = $1::uuid OR organization_id IS NULL
ORDER BY organization_id NULLS LAST, key_name;

-- name: UpsertExtractionKey :one
-- Idempotent upsert: on conflict (org, key_name), updates source_query and
-- data_type so that RETURNING always yields the current row.
INSERT INTO extraction_keys (organization_id, key_name, source_query, data_type)
VALUES (sqlc.narg(organization_id)::uuid, sqlc.arg(key_name), sqlc.arg(source_query), sqlc.arg(data_type))
ON CONFLICT ON CONSTRAINT uq_extraction_keys_org_name DO UPDATE
    SET source_query = EXCLUDED.source_query,
        data_type    = EXCLUDED.data_type
RETURNING *;

-- name: GetExtractionKeysByNames :many
-- Lookup extraction keys by key_name for a tenant. Returns org-specific keys
-- and system keys (organization_id IS NULL) that match the given names.
-- When both a tenant key and a system key share the same key_name, the tenant
-- key is selected (DISTINCT ON + ORDER BY ensures deterministic precedence).
-- Used in the extract callback to map key_name → key_id for bulk data insert.
SELECT DISTINCT ON (key_name) * FROM extraction_keys
WHERE key_name = ANY(sqlc.arg(key_names)::text[])
  AND (organization_id = sqlc.arg(organization_id)::uuid OR organization_id IS NULL)
ORDER BY key_name, (organization_id IS NULL) ASC;

-- name: UpsertExtractedDatum :exec
-- Idempotent upsert: inserts a single extracted key-value pair for a document.
-- On conflict (document_id, key_id) updates the value so repeated worker
-- callbacks are safe and the latest value wins.
INSERT INTO document_extracted_data (organization_id, document_id, key_id, extracted_value)
VALUES ($1, $2, $3, $4)
ON CONFLICT ON CONSTRAINT uq_extracted_data_doc_key DO UPDATE
    SET extracted_value = EXCLUDED.extracted_value;

-- name: BatchUpsertExtractedData :exec
-- Batch idempotent upsert: inserts all extracted key-value pairs for a document
-- in a single statement. Two unnest() calls in the SELECT list are expanded
-- in lockstep by PostgreSQL (guaranteed since PG 10), zipping key_ids with
-- extracted_values row-by-row. FROM unnest(arr, arr) would be cleaner but
-- sqlc does not support multi-arg unnest in the FROM clause. On conflict,
-- latest value wins.
INSERT INTO document_extracted_data (organization_id, document_id, key_id, extracted_value)
SELECT sqlc.arg(organization_id)::uuid,
       sqlc.arg(document_id)::uuid,
       unnest(sqlc.arg(key_ids)::uuid[]),
       unnest(sqlc.arg(extracted_values)::text[])
ON CONFLICT ON CONSTRAINT uq_extracted_data_doc_key DO UPDATE
    SET extracted_value = EXCLUDED.extracted_value;
