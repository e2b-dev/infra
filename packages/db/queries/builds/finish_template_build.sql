-- name: FinishTemplateBuild :exec
-- kernel_version and firecracker_version are overwritten with whatever the
-- template-manager reports back in TemplateBuildMetadata. Old template-managers
-- that do not populate those fields end up passing an empty string; the
-- NULLIF + COALESCE trick leaves the row's existing values untouched in that
-- case so we do not clobber the values the API seeded at build registration.
WITH deactivated AS (
    DELETE FROM public.active_template_builds WHERE build_id = @build_id
)
UPDATE "public"."env_builds"
SET
    finished_at = NOW(),
    total_disk_size_mb = @total_disk_size_mb,
    status = @status,
    envd_version = @envd_version,
    kernel_version = COALESCE(NULLIF(@kernel_version::text, ''), kernel_version),
    firecracker_version = COALESCE(NULLIF(@firecracker_version::text, ''), firecracker_version)
WHERE
    id = @build_id;
