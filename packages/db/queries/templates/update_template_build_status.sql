-- name: UpdateEnvBuildStatus :exec
UPDATE "public"."env_builds"
SET status = @status,
    finished_at = @finished_at,
    reason = sqlc.narg(reason),
    version = @version
WHERE id = @build_id;
