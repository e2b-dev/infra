-- name: UpdateTeam :one
UPDATE public.teams
SET
    name = CASE
        WHEN sqlc.arg(name_set)::bool THEN sqlc.narg(name)::text
        ELSE name
    END,
    profile_picture_url = CASE
        WHEN sqlc.arg(profile_picture_url_set)::bool THEN sqlc.narg(profile_picture_url)::text
        ELSE profile_picture_url
    END
WHERE id = sqlc.arg(team_id)::uuid
RETURNING id, name, profile_picture_url;
