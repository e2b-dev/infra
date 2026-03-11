-- +goose NO TRANSACTION
-- +goose Up

-- Backfill NULL metadata to empty jsonb in batches.
-- Uses cursor-based iteration on the primary key to avoid re-scanning.
-- Resumable: on restart, finds the first NULL row and continues from there.
-- +goose StatementBegin
CREATE OR REPLACE PROCEDURE backfill_snapshots_metadata() AS $$
DECLARE
  batch_size INT := 50000;
  rows_updated INT;
  last_id UUID := '00000000-0000-0000-0000-000000000000';
  current_max_id UUID;
BEGIN
  LOOP
    SELECT id INTO current_max_id
    FROM (
      SELECT id FROM public.snapshots
      WHERE id > last_id AND metadata IS NULL
      ORDER BY id
      LIMIT batch_size
    ) sub
    ORDER BY id DESC
    LIMIT 1;

    EXIT WHEN current_max_id IS NULL;

    UPDATE public.snapshots
    SET metadata = '{}'::jsonb
    WHERE id > last_id AND id <= current_max_id AND metadata IS NULL;

    GET DIAGNOSTICS rows_updated = ROW_COUNT;
    last_id := current_max_id;

    COMMIT;
    RAISE NOTICE 'backfill_snapshots_metadata: updated % rows up to id %, sleeping 10s...', rows_updated, last_id;
    PERFORM pg_sleep(10);
  END LOOP;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CALL backfill_snapshots_metadata();
DROP PROCEDURE backfill_snapshots_metadata();

ALTER TABLE public.snapshots ALTER COLUMN metadata SET DEFAULT '{}'::jsonb;
ALTER TABLE public.snapshots ALTER COLUMN metadata SET NOT NULL;

CREATE EXTENSION IF NOT EXISTS btree_gin;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_snapshots_team_metadata_gin
    ON public.snapshots USING gin (team_id, metadata);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_snapshots_team_metadata_gin;

ALTER TABLE public.snapshots ALTER COLUMN metadata DROP NOT NULL;
ALTER TABLE public.snapshots ALTER COLUMN metadata DROP DEFAULT;
