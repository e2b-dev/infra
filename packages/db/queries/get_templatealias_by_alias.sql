-- name: GetTemplateAliasByAlias :one
SELECT ea.*
FROM "public"."env_aliases" ea
WHERE ea.alias = $1;
