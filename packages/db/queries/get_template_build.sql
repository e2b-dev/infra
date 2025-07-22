-- name: GetTemplateBuild :one
SELECT sqlc.embed(e), sqlc.embed(eb)
FROM "public"."envs" e
JOIN "public"."env_builds" eb ON eb.env_id = e.id
WHERE e.id = sqlc.arg(template_id) AND eb.id = sqlc.arg(build_id);
