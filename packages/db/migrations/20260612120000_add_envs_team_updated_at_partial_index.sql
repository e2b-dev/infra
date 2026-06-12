-- +goose Up
-- +goose NO TRANSACTION

-- Serves the dashboard templates list sorted by updated_at, mirroring
-- idx_envs_team_source_created_at. Partial: only ~1M of the ~96M envs rows
-- are templates (the rest are sandbox snapshots), which keeps the index
-- ~100x smaller and out of the snapshot write path entirely.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_envs_team_updated_at_templates
    ON "public"."envs" (team_id, updated_at DESC, id DESC)
    WHERE source = 'template';

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS idx_envs_team_updated_at_templates;
