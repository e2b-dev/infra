-- name: DeleteTemplate :many
-- Deletes a template and returns its alias cache keys for cache invalidation.
-- Alias keys are captured via CTE before the cascade delete removes them.
WITH alias_keys AS (
  SELECT CASE
    WHEN namespace IS NOT NULL THEN namespace || '/' || alias
    ELSE alias
  END::text AS alias_key
  FROM public.env_aliases
  WHERE env_id = @template_id
), deleted AS (
  DELETE FROM "public"."envs"
  WHERE id = @template_id
  AND team_id = @team_id
  RETURNING id
)
SELECT alias_key FROM alias_keys
WHERE EXISTS (SELECT 1 FROM deleted);