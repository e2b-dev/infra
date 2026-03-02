-- name: GetSnapshotTemplateOriginNode :one
SELECT origin_node_id, build_id
FROM "public"."snapshot_templates"
WHERE env_id = @env_id
LIMIT 1;
