-- +goose Up
-- +goose NO TRANSACTION

-- +goose StatementBegin
-- Add source column to track where the env came from (template build vs snapshot)
-- 'template' = built from Dockerfile (default)
-- 'snapshot' = created from explicit snapshot API
-- 'sandbox' = created from auto-pause feature (internal pause state)
ALTER TABLE "public"."envs"
    ADD COLUMN IF NOT EXISTS "source" text NOT NULL DEFAULT 'template';

-- Create snapshot_templates table to track which sandbox a snapshot was created from
CREATE TABLE IF NOT EXISTS "public"."snapshot_templates" (
    env_id text NOT NULL PRIMARY KEY REFERENCES envs(id) ON DELETE CASCADE,
    sandbox_id text NOT NULL,
    created_at timestamp with time zone DEFAULT now()
);

-- Enable RLS for snapshot_templates table
ALTER TABLE "public"."snapshot_templates" ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- Create index for filtering envs by source (used in GET /snapshots endpoint)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_envs_source
    ON "public"."envs" (source)
    WHERE source = 'snapshot';

-- Create index for looking up snapshots by source sandbox
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_snapshot_templates_sandbox_id
    ON "public"."snapshot_templates" (sandbox_id);

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS idx_snapshot_templates_sandbox_id;
DROP INDEX CONCURRENTLY IF EXISTS idx_envs_source;

-- +goose StatementBegin
DROP TABLE IF EXISTS "public"."snapshot_templates";
ALTER TABLE "public"."envs" DROP COLUMN IF EXISTS "source";
-- +goose StatementEnd
