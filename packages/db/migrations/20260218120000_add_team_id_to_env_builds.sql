-- +goose Up
-- +goose NO TRANSACTION

ALTER TABLE public.env_builds ADD COLUMN IF NOT EXISTS team_id uuid;

-- +goose StatementBegin
-- Trigger to auto-populate team_id when a build is assigned to an env.
-- Mirrors the existing backfill_env_id_from_assignment() trigger pattern.
CREATE OR REPLACE FUNCTION backfill_team_id_from_assignment()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE env_builds
    SET team_id = (SELECT team_id FROM envs WHERE id = NEW.env_id)
    WHERE id = NEW.build_id AND team_id IS NULL;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trigger_backfill_team_id
    AFTER INSERT ON env_build_assignments
    FOR EACH ROW EXECUTE FUNCTION backfill_team_id_from_assignment();

-- +goose StatementBegin
-- Uses env_build_assignments -> envs to resolve team_id (NOT the legacy env_builds.env_id column).
CREATE OR REPLACE PROCEDURE backfill_env_builds_team_id() AS $$
DECLARE
  batch_size INT := 50000;
  rows_updated INT;
BEGIN
  LOOP
    UPDATE public.env_builds eb
    SET team_id = e.team_id
    FROM (
      SELECT eb2.id, e2.team_id
      FROM public.env_builds eb2
      JOIN public.env_build_assignments eba ON eba.build_id = eb2.id
      JOIN public.envs e2 ON e2.id = eba.env_id
      WHERE eb2.team_id IS NULL
      LIMIT batch_size
    ) e
    WHERE eb.id = e.id;
    GET DIAGNOSTICS rows_updated = ROW_COUNT;
    COMMIT;
    EXIT WHEN rows_updated = 0;
  END LOOP;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CALL backfill_env_builds_team_id();
DROP PROCEDURE backfill_env_builds_team_id();

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_env_builds_team_status_group
  ON public.env_builds (team_id, status_group);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_env_builds_team_status
  ON public.env_builds (team_id, status);

-- +goose Down
-- +goose NO TRANSACTION

DROP INDEX CONCURRENTLY IF EXISTS idx_env_builds_team_status;
DROP INDEX CONCURRENTLY IF EXISTS idx_env_builds_team_status_group;
DROP TRIGGER IF EXISTS trigger_backfill_team_id ON env_build_assignments;
DROP FUNCTION IF EXISTS backfill_team_id_from_assignment();
ALTER TABLE public.env_builds DROP COLUMN IF EXISTS team_id;
