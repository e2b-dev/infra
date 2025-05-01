-- name: GetLastSnapshot :one
SELECT sqlc.embed(s), sqlc.embed(eb)
FROM "public"."snapshots" s
JOIN "public"."envs" e ON s.env_id  = e.id
JOIN "public"."env_builds" eb ON e.id = eb.env_id
WHERE s.sandbox_id = $1 AND eb.status = 'success' AND e.team_id = $2
ORDER BY eb.finished_at DESC
LIMIT 1;
