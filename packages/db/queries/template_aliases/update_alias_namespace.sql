-- name: UpdateAliasNamespace :exec
UPDATE "public"."env_aliases"
SET namespace = sqlc.narg(namespace)
WHERE alias = @alias;
