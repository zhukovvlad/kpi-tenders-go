-- name: CreateUserInvitation :one
INSERT INTO user_invitations (organization_id, email, role, invited_by, token_hash, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetUserInvitationByTokenHash :one
-- token_hash is covered by a unique index (idx_user_invitations_token_hash) so at
-- most one row can ever match. organization_id is intentionally omitted: at the
-- token-redemption step the caller has no org context and must look up by token alone.
SELECT * FROM user_invitations WHERE token_hash = $1;

-- name: ListUserInvitationsByOrg :many
SELECT * FROM user_invitations
WHERE organization_id = $1
ORDER BY created_at DESC;

-- name: AcceptUserInvitation :one
-- organization_id is included for defense-in-depth: the caller already holds
-- the full invitation row (from GetUserInvitationByTokenHash) so passing it
-- costs nothing and prevents any theoretical IDOR via a known invitation UUID.
UPDATE user_invitations
SET accepted_at = now()
WHERE id = $1 AND organization_id = $2 AND accepted_at IS NULL AND expires_at > now()
RETURNING *;

-- name: DeleteUserInvitation :execrows
DELETE FROM user_invitations
WHERE id = $1 AND organization_id = $2;
