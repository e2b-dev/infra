-- name: CountTeamSnapshots :one
-- Counts the number of snapshot templates for a team.
-- Snapshot templates are envs with source='snapshot_template'.
SELECT COUNT(*)::bigint
FROM "public"."envs"
WHERE team_id = @team_id
AND source = 'snapshot_template';
