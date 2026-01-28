-- +goose Up
-- +goose StatementBegin

-- Add source column to track where the env came from (template build vs snapshot)
-- 'template' = built from Dockerfile (default)
-- 'snapshot' = created from a running sandbox snapshot
ALTER TABLE "public"."envs"
    ADD COLUMN IF NOT EXISTS "source" text NOT NULL DEFAULT 'template';

-- Add source_sandbox_id to track which sandbox the snapshot was created from
-- NULL for templates built from Dockerfile
ALTER TABLE "public"."envs"
    ADD COLUMN IF NOT EXISTS "source_sandbox_id" text NULL;

-- +goose StatementEnd

-- +goose NO TRANSACTION
-- Create index for listing snapshots by team (used in GET /snapshots endpoint)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_envs_team_source
    ON "public"."envs" (team_id, source)
    WHERE source = 'snapshot';

-- Create index for looking up snapshots by source sandbox
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_envs_source_sandbox_id
    ON "public"."envs" (source_sandbox_id)
    WHERE source_sandbox_id IS NOT NULL;

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS idx_envs_source_sandbox_id;
DROP INDEX CONCURRENTLY IF EXISTS idx_envs_team_source;

-- +goose StatementBegin
ALTER TABLE "public"."envs" DROP COLUMN IF EXISTS "source_sandbox_id";
ALTER TABLE "public"."envs" DROP COLUMN IF EXISTS "source";
-- +goose StatementEnd
