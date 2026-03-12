-- +goose NO TRANSACTION
-- +goose Up

-- Fix rows where metadata contains the JSON literal 'null' (not SQL NULL).
-- These are not caught by the previous IS NULL backfill but fail @> '{}' checks.
-- +goose StatementBegin
CREATE OR REPLACE PROCEDURE fix_snapshots_jsonb_null_metadata() AS $$
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
      WHERE id > last_id AND metadata = 'null'::jsonb
      ORDER BY id
      LIMIT batch_size
    ) sub
    ORDER BY id DESC
    LIMIT 1;

    EXIT WHEN current_max_id IS NULL;

    UPDATE public.snapshots
    SET metadata = '{}'::jsonb
    WHERE id > last_id AND id <= current_max_id AND metadata = 'null'::jsonb;

    GET DIAGNOSTICS rows_updated = ROW_COUNT;
    last_id := current_max_id;

    COMMIT;
    RAISE NOTICE 'fix_snapshots_jsonb_null_metadata: updated % rows up to id %', rows_updated, last_id;
    IF rows_updated > 0 THEN
      PERFORM pg_sleep(10);
    END IF;
  END LOOP;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CALL fix_snapshots_jsonb_null_metadata();
DROP PROCEDURE fix_snapshots_jsonb_null_metadata();

-- Also add a CHECK constraint to prevent both SQL NULL and JSON null going forward.
ALTER TABLE public.snapshots
  ADD CONSTRAINT chk_snapshots_metadata_not_json_null
  CHECK (metadata != 'null'::jsonb) NOT VALID;

ALTER TABLE public.snapshots
  VALIDATE CONSTRAINT chk_snapshots_metadata_not_json_null;

-- +goose Down
ALTER TABLE public.snapshots DROP CONSTRAINT IF EXISTS chk_snapshots_metadata_not_json_null;
