-- name: GetEnvWithBuild :one
SELECT sqlc.embed(e), sqlc.embed(eb)
FROM "public"."envs" e
LEFT JOIN "public"."env_aliases" ea ON ea.env_id = e.id
JOIN "public"."env_builds" eb ON e.id = eb.env_id
WHERE e.id = $1 OR ea.alias = $1
ORDER BY eb.finished_at DESC
LIMIT 1;
