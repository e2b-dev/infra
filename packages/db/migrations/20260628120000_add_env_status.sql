-- +goose Up
-- Soft-delete marker for envs (templates, snapshots, snapshot_templates). On
-- user delete the env is marked 'deleted' instead of being removed, so its
-- build assignments and snapshot rows survive and the build lineage stays
-- traceable. Reads filter out status='deleted'.
ALTER TABLE "public"."envs" ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'active';

-- +goose Down
ALTER TABLE "public"."envs" DROP COLUMN IF EXISTS status;
