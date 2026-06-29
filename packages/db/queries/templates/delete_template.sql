-- name: DeleteTemplate :many
-- Soft-deletes a template by marking its env status='deleted'. The env row, its
-- build assignments, and any snapshot rows are preserved so the build lineage
-- stays traceable for a future storage GC. Aliases are released (deleted) so the
-- name can be reused, and active_template_builds rows are cleared (mirroring the
-- old env cascade) so in-flight build tracking and concurrency counts don't
-- linger. Returns the released alias cache keys for cache invalidation.
WITH alias_keys AS (
  SELECT CASE
    WHEN ea.namespace IS NOT NULL THEN ea.namespace || '/' || ea.alias
    ELSE ea.alias
  END::text AS alias_key
  FROM public.env_aliases ea
  WHERE ea.env_id = @template_id
), updated AS (
  UPDATE public.envs e
  SET status = 'deleted', updated_at = NOW()
  WHERE e.id = @template_id
  AND e.team_id = @team_id
  AND e.status <> 'deleted'
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
