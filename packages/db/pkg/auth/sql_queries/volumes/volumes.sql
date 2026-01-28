-- name: CreateVolume :one
INSERT INTO volumes (team_id, name)
VALUES (@team_id, @name)
RETURNING *;

-- name: GetVolume :one
SELECT * FROM volumes WHERE id = @volume_id;

-- name: FindVolumesByTeamID :many
SELECT * FROM volumes WHERE team_id = @team_id;

-- name: UpdateVolume :one
UPDATE volumes
SET name = @name
WHERE id = @volume_id
RETURNING *;
