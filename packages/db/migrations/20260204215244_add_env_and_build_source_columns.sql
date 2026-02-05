-- +goose Up
-- +goose NO TRANSACTION

-- +goose StatementBegin
-- Add source column to envs table to track where the env came from
-- 'template' = built from Dockerfile (default)
-- 'snapshot' = created from pause/resume
ALTER TABLE "public"."envs"
    ADD COLUMN IF NOT EXISTS "source" text NOT NULL DEFAULT 'template';

-- Create trigger first so any concurrent snapshot inserts are handled
CREATE OR REPLACE FUNCTION sync_env_source_on_snapshot_insert() RETURNS TRIGGER AS $$
BEGIN
    UPDATE "public"."envs" SET source = 'snapshot' WHERE id = NEW.env_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_sync_env_source_on_snapshot ON "public"."snapshots";
CREATE TRIGGER trg_sync_env_source_on_snapshot
AFTER INSERT ON "public"."snapshots"
FOR EACH ROW EXECUTE FUNCTION sync_env_source_on_snapshot_insert();

-- Then backfill existing snapshot envs
UPDATE "public"."envs" SET source = 'snapshot'
WHERE id IN (SELECT env_id FROM "public"."snapshots");
-- +goose StatementEnd

-- Create index (using CONCURRENTLY requires NO TRANSACTION)
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_envs_source
    ON "public"."envs" (source);

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS idx_envs_source;

-- +goose StatementBegin
DROP TRIGGER IF EXISTS trg_sync_env_source_on_snapshot ON "public"."snapshots";
DROP FUNCTION IF EXISTS sync_env_source_on_snapshot_insert();
ALTER TABLE "public"."envs" DROP COLUMN IF EXISTS "source";
-- +goose StatementEnd
