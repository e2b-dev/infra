-- name: GetSnapshotBuilds :many
SELECT sqlc.embed(s), sqlc.embed(eb) FROM  "public"."snapshots" s
LEFT JOIN "public"."env_builds" eb ON s."env_id" = eb."env_id"
WHERE s.sandbox_id = @sandbox_id
AND s.team_id = @team_id;
