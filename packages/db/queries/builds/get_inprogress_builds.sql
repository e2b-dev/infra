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
-- Relies on active_template_builds table (migration 20260305130000).
SELECT COUNT(*) as build_count
FROM public.active_template_builds atb
WHERE atb.team_id = sqlc.arg(team_id)::uuid
  AND atb.created_at > NOW() - INTERVAL '1 day'
  AND NOT (
    atb.template_id = sqlc.arg(exclude_template_id)::text
    AND atb.tags && sqlc.arg(exclude_tags)::text[]
  );

-- name: GetCancellableTemplateBuildsByTeam :many
-- Relies on idx_env_builds_team_active partial index (migration 20260305120000).
SELECT DISTINCT ON (b.id) b.id as build_id, e.id as template_id, e.cluster_id, b.cluster_node_id
FROM public.env_builds b
JOIN public.env_build_assignments eba ON eba.build_id = b.id
JOIN public.envs e ON e.id = eba.env_id
WHERE b.team_id = $1
  AND b.status_group IN ('pending', 'in_progress')
  AND e.source = 'template'
ORDER BY b.id;
