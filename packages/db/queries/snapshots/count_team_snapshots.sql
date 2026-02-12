-- name: CountTeamSnapshots :one
-- Counts the total number of snapshot templates for a team.
SELECT COUNT(*)::integer as count
FROM "public"."envs" e
WHERE e.team_id = @team_id
AND e.source = 'snapshot_template';
