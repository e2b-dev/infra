-- name: CheckAliasConflictsWithTemplateID :one
SELECT EXISTS(
    SELECT 1
    FROM "public"."envs"
    WHERE id = @alias
);

-- name: CheckAliasExistsInNamespace :one
-- Check if alias exists within a specific namespace.
-- Used for namespace-aware lookups. Returns the alias if found.
SELECT *
FROM "public"."env_aliases"
WHERE alias = @alias
  AND namespace IS NOT DISTINCT FROM sqlc.narg(namespace)::text;