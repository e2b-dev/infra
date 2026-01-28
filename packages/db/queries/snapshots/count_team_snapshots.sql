-- name: CountTeamSnapshots :one
-- Counts the total number of snapshots for a team (for pagination).
SELECT COUNT(*)::int as count
FROM "public"."envs"
WHERE team_id = @team_id
AND source = 'snapshot';
