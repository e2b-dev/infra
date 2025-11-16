-- name: GetInProgressTemplateBuilds :many
SELECT sqlc.embed(t), sqlc.embed(e), sqlc.embed(b)
FROM public.env_builds b
JOIN public.envs e ON e.id = b.env_id
JOIN public.teams t ON e.team_id = t.id
WHERE b.status = 'waiting' OR b.status = 'building'
ORDER BY b.created_at DESC;

