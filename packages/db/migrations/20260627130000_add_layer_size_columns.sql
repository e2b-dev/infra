-- +goose Up
-- Memfile logical (virtual device) size captured synchronously at snapshot time.
-- The rootfs logical size is already stored as total_disk_size_mb. Mapped and
-- diff sizes (for both rootfs and memfile) are kept in the data objects' custom
-- metadata instead, so each size category lives entirely in one place.
ALTER TABLE public.env_builds
  ADD COLUMN IF NOT EXISTS memfile_logical_size_bytes bigint;

-- +goose Down
ALTER TABLE public.env_builds
  DROP COLUMN IF EXISTS memfile_logical_size_bytes;
