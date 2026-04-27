-- name: CreateExtractionKey :one
INSERT INTO extraction_keys (organization_id, key_name, source_query, description, data_type, is_required)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetExtractionKeyByOrgAndKeyName :one
SELECT *
FROM extraction_keys
WHERE organization_id IS NOT DISTINCT FROM $1
  AND key_name = $2;

-- name: GetExtractionKeyByOrgAndSourceQuery :one
SELECT *
FROM extraction_keys
WHERE organization_id IS NOT DISTINCT FROM $1
  AND lower(btrim(source_query)) = lower(btrim(sqlc.arg(source_query)))
ORDER BY created_at ASC
LIMIT 1;

-- name: ListExtractionKeysByOrganization :many
SELECT *
FROM extraction_keys
WHERE organization_id IS NOT DISTINCT FROM $1
ORDER BY created_at ASC;

-- name: ListExtractionKeyPayloadsByOrganization :many
SELECT id, key_name, source_query, description, data_type, is_required
FROM extraction_keys
WHERE organization_id IS NOT DISTINCT FROM $1
ORDER BY created_at ASC;

-- name: UpsertDocumentExtractedData :one
INSERT INTO document_extracted_data (organization_id, document_id, key_id, extracted_value, confidence)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (organization_id, document_id, key_id)
DO UPDATE SET
    extracted_value = EXCLUDED.extracted_value,
    confidence      = EXCLUDED.confidence,
    updated_at      = now()
RETURNING *;

-- name: GetDocumentOrganizationID :one
SELECT organization_id
FROM documents
WHERE id = $1;
