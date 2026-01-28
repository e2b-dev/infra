-- name: CountCheckpoints :one
-- Counts the number of checkpoints for a sandbox (for enforcing limits).
-- Excludes the 'default' tag which is used for auto-pause.
SELECT COUNT(*)::int as count
FROM "public"."snapshots" s
JOIN "public"."env_build_assignments" eba ON eba.env_id = s.env_id
WHERE s.sandbox_id = @sandbox_id
AND s.team_id = @team_id
AND eba.tag != 'default';
