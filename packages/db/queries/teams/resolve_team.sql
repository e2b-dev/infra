-- name: ResolveTeamBySlugAndUser :one
SELECT t.id, t.slug
FROM public.teams t
JOIN public.users_teams ut ON ut.team_id = t.id
WHERE ut.user_id = sqlc.arg(user_id)::uuid
  AND t.slug = sqlc.arg(slug)::text;
