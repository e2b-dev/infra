-- +goose Up
-- Soft-delete marker for envs (templates, snapshots, snapshot_templates). On
-- user delete the env is flagged deleted instead of being removed, so its build
-- assignments and snapshot rows survive and the build lineage stays traceable.
ALTER TABLE "public"."envs" ADD COLUMN IF NOT EXISTS deleted boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE "public"."envs" DROP COLUMN IF EXISTS deleted;
