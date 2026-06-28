-- name: SetEnvBuildLayerSizes :exec
-- Persists the synchronously-available per-artifact layer sizes for a build.
-- Each value is nullable so partial data (e.g. filesystem-only snapshots with no
-- memfile) can be written.
UPDATE "public"."env_builds"
SET rootfs_mapped_size_bytes   = sqlc.narg(rootfs_mapped_size_bytes),
    rootfs_diff_size_bytes     = sqlc.narg(rootfs_diff_size_bytes),
    memfile_logical_size_bytes = sqlc.narg(memfile_logical_size_bytes)
WHERE id = @build_id;
