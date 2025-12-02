-- name: GetSnapshotBuilds :many
SELECT s.env_id as template_id, eb.id as build_id, eb.cluster_node_id as build_cluster_node_id FROM  "public"."snapshots" s
LEFT JOIN "public"."env_builds" eb ON s."env_id" = eb."env_id"
WHERE s.sandbox_id = @sandbox_id
AND s.team_id = @team_id;
