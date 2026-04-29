-- name: DeleteTemplate :many
-- Deletes a template and returns alias cache keys and active builds.
-- Both are captured via CTEs before the cascade delete removes them.
-- Active builds are returned so the caller can stop them on the orchestrator.
WITH alias_keys AS (
  SELECT CASE
    WHEN namespace IS NOT NULL THEN namespace || '/' || alias
    ELSE alias
  END::text AS alias_key
  FROM public.env_aliases ea
  WHERE ea.env_id = @template_id
), active_builds AS (
  SELECT atb.build_id, e.cluster_id, b.cluster_node_id
  FROM public.active_template_builds atb
  JOIN public.env_builds b ON b.id = atb.build_id
  JOIN public.envs e ON e.id = atb.template_id
  WHERE atb.template_id = @template_id
), deleted AS (
  DELETE FROM "public"."envs" envs_del
  WHERE envs_del.id = @template_id
  AND envs_del.team_id = @team_id
  RETURNING envs_del.id
)
SELECT alias_key, NULL::uuid AS build_id, NULL::uuid AS cluster_id, NULL::text AS cluster_node_id
FROM alias_keys WHERE EXISTS (SELECT 1 FROM deleted)
UNION ALL
SELECT ''::text AS alias_key, build_id, cluster_id, cluster_node_id
FROM active_builds WHERE EXISTS (SELECT 1 FROM deleted);
