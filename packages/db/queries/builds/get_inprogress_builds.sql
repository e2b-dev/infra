-- name: GetInProgressTemplateBuilds :many
SELECT DISTINCT ON (b.id) sqlc.embed(t), sqlc.embed(e), sqlc.embed(b)
FROM public.env_builds b
JOIN public.env_build_assignments eba ON eba.build_id = b.id
JOIN public.envs e ON e.id = eba.env_id
JOIN public.teams t ON e.team_id = t.id
WHERE b.status_group IN ('pending', 'in_progress')
  AND e.source = 'template'
ORDER BY b.id, b.created_at DESC;

-- name: GetInProgressTemplateBuildsByTeam :one
SELECT COUNT(DISTINCT b.id) as build_count
FROM public.env_builds b
JOIN public.env_build_assignments eba ON eba.build_id = b.id
JOIN public.envs e ON e.id = eba.env_id
WHERE b.team_id = @team_id
  AND b.status_group IN ('pending', 'in_progress')
  AND e.source = 'template'
  AND NOT EXISTS (
    SELECT 1 FROM public.env_build_assignments exc
    WHERE exc.build_id = b.id
      AND exc.env_id = @exclude_template_id
      AND exc.tag = ANY(@exclude_tags::text[])
  );

-- name: GetCancellableTemplateBuildsByTeam :many
SELECT DISTINCT ON (b.id) b.id as build_id, e.id as template_id, e.cluster_id, b.cluster_node_id
FROM public.env_builds b
JOIN public.env_build_assignments eba ON eba.build_id = b.id
JOIN public.envs e ON e.id = eba.env_id
WHERE b.team_id = $1
  AND b.status_group IN ('pending', 'in_progress')
  AND e.source = 'template'
ORDER BY b.id;
