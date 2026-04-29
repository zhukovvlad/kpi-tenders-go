-- name: CreateExtractionRequest :one
-- Creates a new extraction request for a document. Tenant isolation is enforced
-- by the composite FK (document_id, organization_id) → documents.
-- Returns the created request including generated id and timestamps.
INSERT INTO extraction_requests (document_id, organization_id, questions, anonymize)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetExtractionRequest :one
-- Tenant-scoped lookup by id; callers should map pgx.ErrNoRows to 404.
SELECT * FROM extraction_requests
WHERE id = $1 AND organization_id = $2;

-- name: GetExtractionRequestByID :one
-- Internal lookup without org check; callers (worker callbacks) must be
-- authenticated via SERVICE_TOKEN and trust the id from a prior tenant-scoped
-- write (document_tasks.extraction_request_id).
SELECT * FROM extraction_requests
WHERE id = $1;

-- name: ListPendingExtractionRequestsByDocument :many
-- Returns extraction requests for a document that are still in flight
-- (status pending or running). Used by WorkerService after prerequisite
-- tasks (convert/anonymize) complete to progress all dependent requests.
SELECT * FROM extraction_requests
WHERE document_id = $1
  AND status IN ('pending', 'running')
ORDER BY created_at ASC;

-- name: SetExtractionRequestStatus :one
-- Updates status (and optionally error_message) for a request.
-- Used to transition pending → running on resolve_keys enqueue,
-- running → completed when extract finishes,
-- and pending|running → failed when a prerequisite task fails fatally.
UPDATE extraction_requests
SET status        = $2,
    error_message = COALESCE($3, error_message),
    updated_at    = now()
WHERE id = $1
RETURNING *;

-- name: SetExtractionRequestResolvedSchema :one
-- Stores resolved_schema returned by resolve_keys so the GET endpoint can
-- enumerate which keys' answers belong to this request. Does not change status.
UPDATE extraction_requests
SET resolved_schema = $2,
    updated_at      = now()
WHERE id = $1
RETURNING *;
