-- name: GetTemplateAliasByAlias :one
SELECT ea.*, e.team_id, e.public
FROM "public"."env_aliases" ea
JOIN "public"."envs" e ON ea.env_id = e.id
WHERE ea.alias = $1;
