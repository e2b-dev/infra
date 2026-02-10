-- +goose Up
ALTER TABLE public.env_builds
  ADD COLUMN status_group text GENERATED ALWAYS AS (
    CASE
      WHEN status IN ('pending', 'waiting') THEN 'pending'
      WHEN status IN ('in_progress', 'building', 'snapshotting') THEN 'in_progress'
      WHEN status IN ('ready', 'uploaded', 'success') THEN 'ready'
      WHEN status = 'failed' THEN 'failed'
    END
  ) STORED;

CREATE INDEX idx_env_builds_status_group ON public.env_builds(status_group);

-- +goose Down
DROP INDEX IF EXISTS idx_env_builds_status_group;
ALTER TABLE public.env_builds DROP COLUMN IF EXISTS status_group;
