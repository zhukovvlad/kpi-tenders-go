-- name: ListExtractionKeysByOrg :many
-- Returns all keys visible to the given tenant: org-specific keys AND system
-- keys (organization_id IS NULL) shared across all tenants.
SELECT * FROM extraction_keys
WHERE organization_id = $1::uuid OR organization_id IS NULL
ORDER BY organization_id NULLS LAST, key_name;

-- name: GetExtractionKey :one
-- Tenant-scoped lookup; returns org-specific or system key by id.
-- Callers should map pgx.ErrNoRows to 404.
SELECT * FROM extraction_keys
WHERE id = sqlc.arg(id)
  AND (organization_id = sqlc.arg(organization_id)::uuid OR organization_id IS NULL);

-- name: CreateExtractionKey :one
-- Creates an org-specific extraction key. Fails on duplicate (org, key_name).
INSERT INTO extraction_keys (organization_id, key_name, source_query, data_type, display_name)
VALUES (sqlc.arg(organization_id)::uuid, sqlc.arg(key_name), sqlc.arg(source_query), sqlc.arg(data_type), sqlc.narg(display_name))
RETURNING *;

-- name: UpdateExtractionKey :one
-- Partial update for org-specific extraction keys; system keys (org IS NULL) are read-only.
-- Uses COALESCE for patch semantics: NULL arguments preserve the existing value.
UPDATE extraction_keys
SET source_query = COALESCE(sqlc.narg(source_query), source_query),
    data_type    = COALESCE(sqlc.narg(data_type), data_type),
    display_name = COALESCE(sqlc.narg(display_name), display_name)
WHERE id              = sqlc.arg(id)
  AND organization_id = sqlc.arg(organization_id)::uuid
RETURNING *;

-- name: DeleteExtractionKey :execrows
-- Deletes an org-specific extraction key. System keys (org IS NULL) cannot be deleted via this query.
DELETE FROM extraction_keys
WHERE id              = sqlc.arg(id)
  AND organization_id = sqlc.arg(organization_id)::uuid;

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
-- On conflict (organization_id, document_id, key_id) updates the value so repeated worker
-- callbacks are safe and the latest value wins.
INSERT INTO document_extracted_data (organization_id, document_id, key_id, extracted_value)
VALUES ($1, $2, $3, $4)
ON CONFLICT ON CONSTRAINT uq_extracted_data_doc_key DO UPDATE
    SET extracted_value = EXCLUDED.extracted_value;

-- name: ListExtractedDataForKeys :many
-- Returns extracted values for a document filtered to the given extraction
-- keys, joined with key metadata. Tenant-scoped: only data and keys visible
-- to the given organization (org-specific keys + system keys) are returned.
-- Used by GET /extraction-requests/:id to assemble the answers map for a
-- specific request's resolved_schema.
SELECT k.key_name,
       k.data_type,
       d.extracted_value
FROM document_extracted_data d
JOIN extraction_keys k ON k.id = d.key_id
WHERE d.document_id     = sqlc.arg(document_id)::uuid
  AND d.organization_id = sqlc.arg(organization_id)::uuid
  AND k.key_name        = ANY(sqlc.arg(key_names)::text[])
ORDER BY k.key_name;

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

-- name: ListExtractedDataByDocument :many
-- Returns all extracted data for a document joined with key metadata.
-- Tenant-scoped: only data belonging to the given org is returned.
-- Used by GET /documents/:id/answers.
SELECT ded.id,
       ded.document_id,
       ded.extracted_value,
       k.id              AS key_id,
       k.organization_id AS key_organization_id,
       k.key_name,
       k.source_query,
       k.data_type,
       k.display_name,
       k.created_at      AS key_created_at
FROM document_extracted_data ded
JOIN extraction_keys k ON k.id = ded.key_id
WHERE ded.document_id     = sqlc.arg(document_id)::uuid
  AND ded.organization_id = sqlc.arg(organization_id)::uuid
ORDER BY k.key_name;
