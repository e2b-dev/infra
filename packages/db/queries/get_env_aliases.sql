-- name: GetEnvAliases :many
SELECT sqlc.embed(ea)
FROM "public"."env_aliases" ea
WHERE ea.env_id = $1;
