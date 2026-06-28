-- name: SetEnvBuildLayerSizes :exec
-- Persists the synchronously-available memfile logical size for a build. Nullable
-- so a filesystem-only snapshot (no memfile) can be written as NULL.
UPDATE "public"."env_builds"
SET memfile_logical_size_bytes = sqlc.narg(memfile_logical_size_bytes)
WHERE id = @build_id;
