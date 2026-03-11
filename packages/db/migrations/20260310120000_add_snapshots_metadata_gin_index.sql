-- +goose NO TRANSACTION
-- +goose Up

-- Backfill NULL metadata to empty jsonb so the NOT NULL constraint and
-- the @> '{}' pattern work correctly.
UPDATE public.snapshots SET metadata = '{}'::jsonb WHERE metadata IS NULL;

ALTER TABLE public.snapshots ALTER COLUMN metadata SET DEFAULT '{}'::jsonb;
ALTER TABLE public.snapshots ALTER COLUMN metadata SET NOT NULL;

CREATE EXTENSION IF NOT EXISTS btree_gin;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_snapshots_team_metadata_gin
    ON public.snapshots USING gin (team_id, metadata);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_snapshots_team_metadata_gin;

ALTER TABLE public.snapshots ALTER COLUMN metadata DROP NOT NULL;
ALTER TABLE public.snapshots ALTER COLUMN metadata DROP DEFAULT;
