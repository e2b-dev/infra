-- name: DeleteTemplate :many
-- Soft-deletes a template: flags its env deleted instead of removing the row, so
-- env_builds, env_build_assignments, and snapshot rows survive for lineage and a
-- future storage GC. Aliases are released and active_template_builds cleared
-- (mirroring the old env cascade). Returns released alias cache keys.
WITH alias_keys AS (
  SELECT CASE
    WHEN ea.namespace IS NOT NULL THEN ea.namespace || '/' || ea.alias
    ELSE ea.alias
  END::text AS alias_key
  FROM public.env_aliases ea
  WHERE ea.env_id = @template_id
), updated AS (
  UPDATE public.envs e
  SET deleted = true, updated_at = NOW()
  WHERE e.id = @template_id
  AND e.team_id = @team_id
  AND e.deleted = false
  RETURNING e.id
), released AS (
  DELETE FROM public.env_aliases ea
  WHERE ea.env_id IN (SELECT id FROM updated)
), deactivated AS (
  DELETE FROM public.active_template_builds atb
  WHERE atb.template_id IN (SELECT id FROM updated)
)
SELECT alias_key FROM alias_keys
WHERE EXISTS (SELECT 1 FROM updated);
