-- name: GetRunningEnvBuilds :many
SELECT sqlc.embed(eb), sqlc.embed(e), sqlc.embed(t)
FROM "public"."env_builds" AS eb
JOIN "public"."envs" AS e ON eb.env_id = e.id
JOIN "public"."teams" AS t ON e.team_id = t.id
WHERE eb.status IN ('waiting', 'building')
ORDER BY eb.created_at DESC;
