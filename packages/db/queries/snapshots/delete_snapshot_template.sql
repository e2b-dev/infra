-- name: DeleteSnapshotTemplate :exec
-- Deletes a persistent snapshot.
-- The env_builds and env_build_assignments are deleted by CASCADE.
DELETE FROM "public"."envs"
WHERE id = @snapshot_id
AND team_id = @team_id
AND source = 'snapshot';
