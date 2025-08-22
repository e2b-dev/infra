-- name: UpdateSnapshotMetadata :many
UPDATE "public"."snapshots" s
SET metadata = $3
FROM "public"."envs" e
WHERE s.env_id = e.id
  AND s.sandbox_id = $1
  AND e.team_id = $2 RETURNING s.id;