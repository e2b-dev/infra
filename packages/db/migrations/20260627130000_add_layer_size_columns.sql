-- +goose Up
-- Per-artifact layer sizes captured synchronously at snapshot time. The rootfs
-- logical size is already stored as total_disk_size_mb. Memfile mapped/diff
-- sizes are intentionally not stored here (they require the async dedup header);
-- they live in the memfile data object's metadata.
ALTER TABLE public.env_builds
  ADD COLUMN IF NOT EXISTS rootfs_mapped_size_bytes bigint,
  ADD COLUMN IF NOT EXISTS rootfs_diff_size_bytes bigint,
  ADD COLUMN IF NOT EXISTS memfile_logical_size_bytes bigint;

-- +goose Down
ALTER TABLE public.env_builds
  DROP COLUMN IF EXISTS rootfs_mapped_size_bytes,
  DROP COLUMN IF EXISTS rootfs_diff_size_bytes,
  DROP COLUMN IF EXISTS memfile_logical_size_bytes;
