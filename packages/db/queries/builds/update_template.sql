-- name: UpdateTemplateBuild :exec
UPDATE "public"."env_builds"
SET
    start_cmd = @start_cmd,
    ready_cmd = @ready_cmd,
    dockerfile = @dockerfile,
    cluster_node_id = @cluster_node_id
WHERE
    id = @build_uuid;