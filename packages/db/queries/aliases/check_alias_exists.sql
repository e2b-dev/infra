-- name: CheckAliasConflictsWithTemplate :one
SELECT EXISTS(
    SELECT 1
    FROM "public"."envs"
    WHERE id = @alias
);

-- name: CheckAliasExists :one
SELECT *
FROM "public"."env_aliases"
WHERE alias = @alias;