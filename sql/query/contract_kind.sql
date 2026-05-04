-- name: ListContractKindsByOrg :many
-- Returns all contract kinds visible to the given tenant: org-specific AND system kinds (organization_id IS NULL).
SELECT * FROM document_contract_kinds
WHERE organization_id = $1::uuid OR organization_id IS NULL
ORDER BY sort_order ASC, display_name ASC;

-- name: GetContractKind :one
SELECT * FROM document_contract_kinds
WHERE id = $1
  AND (organization_id = $2::uuid OR organization_id IS NULL);

-- name: CreateContractKind :one
INSERT INTO document_contract_kinds (organization_id, display_name, sort_order, is_active)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: UpdateContractKind :one
UPDATE document_contract_kinds
SET display_name = $3,
    sort_order   = $4,
    is_active    = $5,
    updated_at   = now()
WHERE id = $1 AND organization_id = $2
RETURNING *;

-- name: DeleteContractKind :execrows
DELETE FROM document_contract_kinds
WHERE id = $1 AND organization_id = $2;
