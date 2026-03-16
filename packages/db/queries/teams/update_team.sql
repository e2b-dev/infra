-- name: UpdateTeamName :one
UPDATE public.teams
SET name = sqlc.arg(name)::text
WHERE id = sqlc.arg(team_id)::uuid
RETURNING id, name;
