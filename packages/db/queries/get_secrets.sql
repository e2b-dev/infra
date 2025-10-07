-- name: GetTeamSecrets :many
SELECT
    id,
    label,
    description,
    allowlist,
    created_at
FROM "public"."secrets"
WHERE team_id = @team_id
ORDER BY created_at DESC;

