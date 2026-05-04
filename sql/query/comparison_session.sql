-- name: CreateComparisonSession :one
INSERT INTO comparison_sessions (organization_id, created_by, name, contract_kind_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetComparisonSession :one
SELECT * FROM comparison_sessions
WHERE id = $1 AND organization_id = $2;

-- name: ListComparisonSessionsByOrg :many
SELECT * FROM comparison_sessions
WHERE organization_id = $1
ORDER BY created_at DESC;

-- name: DeleteComparisonSession :execrows
DELETE FROM comparison_sessions
WHERE id = $1 AND organization_id = $2;

-- name: AddDocumentToComparisonSession :one
INSERT INTO comparison_session_documents (session_id, document_id, organization_id, position)
VALUES ($1, $2, $3, $4)
ON CONFLICT (session_id, document_id) DO UPDATE
    SET position = EXCLUDED.position
RETURNING *;

-- name: RemoveDocumentFromComparisonSession :execrows
DELETE FROM comparison_session_documents
WHERE session_id = $1 AND document_id = $2;

-- name: ListComparisonSessionDocuments :many
SELECT * FROM comparison_session_documents
WHERE session_id = $1
ORDER BY position ASC;
