-- +goose Up
-- Soft-delete marker for envs, so deleting a template/snapshot keeps its build
-- assignments and snapshot rows instead of cascading them away.
ALTER TABLE "public"."envs" ADD COLUMN IF NOT EXISTS deleted boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE "public"."envs" DROP COLUMN IF EXISTS deleted;
