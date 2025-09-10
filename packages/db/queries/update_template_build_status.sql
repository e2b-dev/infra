-- name: UpdateEnvBuildStatus :exec
UPDATE "public"."env_builds"
SET status = @status,
    finished_at = @finished_at,
    reason = sqlc.narg(reason)
WHERE id = @build_id AND env_id = @template_id;
