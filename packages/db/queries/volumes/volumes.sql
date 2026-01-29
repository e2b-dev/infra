-- name: CreateVolume :one
INSERT INTO volumes (team_id, volume_type, name)
VALUES (@team_id, @volume_type, @name)
RETURNING *;

-- name: GetVolume :one
SELECT * FROM volumes WHERE id = @volume_id AND team_id = @team_id;

-- name: GetVolumesByName :many
SELECT * FROM volumes WHERE team_id = @team_id AND name IN (
    SELECT UNNEST(@volume_names::text[])
);

-- name: FindVolumesByTeamID :many
SELECT * FROM volumes WHERE team_id = @team_id;

-- name: UpdateVolume :one
UPDATE volumes
SET name = @name
WHERE id = @volume_id
RETURNING *;
