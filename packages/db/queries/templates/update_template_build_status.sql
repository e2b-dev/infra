-- name: UpdateEnvBuildStatus :exec
UPDATE "public"."env_builds"
SET status = @status,
    finished_at = @finished_at,
    reason = sqlc.narg(reason),
    version = @version
WHERE id = @build_id;

-- name: FailTemplateBuildAndDeactivate :exec
WITH deactivated AS (
    DELETE FROM public.active_template_builds WHERE build_id = @build_id
)
UPDATE "public"."env_builds"
SET status = @status,
    finished_at = @finished_at,
    reason = sqlc.narg(reason),
    version = @version
WHERE id = @build_id;
