-- name: FinishTemplateBuild :exec
WITH deactivated AS (
    DELETE FROM public.active_template_builds WHERE build_id = @build_id
)
UPDATE "public"."env_builds"
SET
    finished_at = NOW(),
    total_disk_size_mb = @total_disk_size_mb,
    status = @status,
    envd_version = @envd_version
WHERE
    id = @build_id;
