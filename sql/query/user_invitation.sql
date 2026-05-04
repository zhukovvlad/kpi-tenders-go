-- name: CreateUserInvitation :one
INSERT INTO user_invitations (organization_id, email, role, invited_by, token_hash, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUserInvitationByTokenHash :one
SELECT * FROM user_invitations WHERE token_hash = $1;

-- name: ListUserInvitationsByOrg :many
SELECT * FROM user_invitations
WHERE organization_id = $1
ORDER BY created_at DESC;

-- name: AcceptUserInvitation :one
UPDATE user_invitations
SET accepted_at = now()
WHERE id = $1 AND accepted_at IS NULL
RETURNING *;

-- name: DeleteUserInvitation :execrows
DELETE FROM user_invitations
WHERE id = $1 AND organization_id = $2;
