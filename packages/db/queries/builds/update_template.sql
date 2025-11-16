-- name: UpdateTemplateBuild :exec
UPDATE "public"."env_builds"
SET
    start_cmd = @start_cmd,
    ready_cmd = @ready_cmd,
    dockerfile = @dockerfile
WHERE
    id = @build_uuid;