-- name: UpdateTeam :one
UPDATE public.teams
SET
    name = COALESCE(sqlc.narg(name)::text, name),
    profile_picture_url = COALESCE(sqlc.narg(profile_picture_url)::text, profile_picture_url)
WHERE id = sqlc.arg(team_id)::uuid
RETURNING id, name, profile_picture_url;
