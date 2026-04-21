-- name: CreateVolume :one
INSERT INTO volumes (team_id, volume_type, name, volume_path)
VALUES (@team_id, @volume_type, @name, @volume_path)
RETURNING *;

-- name: GetVolume :one
SELECT * FROM volumes WHERE id = @volume_id AND team_id = @team_id;

-- name: GetVolumesByName :many
SELECT * FROM volumes WHERE team_id = @team_id AND name IN (
    SELECT UNNEST(@volume_names::text[])
);

-- name: FindVolumesByTeamID :many
SELECT * FROM volumes WHERE team_id = @team_id;

-- name: DeleteVolume :exec
DELETE FROM volumes WHERE team_id = @team_id AND id = @volume_id;

-- name: UpdateVolumePath :exec
UPDATE volumes SET volume_path = @volume_path WHERE id = @volume_id AND team_id = @team_id;
