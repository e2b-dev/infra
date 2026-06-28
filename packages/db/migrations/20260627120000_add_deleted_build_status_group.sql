-- +goose Up
-- Map the new 'deleted' build status to a dedicated 'deleted' status group so
-- soft-deleted build layers are distinguishable from 'failed'. The trigger
-- already references this function by name; replacing it is sufficient.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION compute_status_group() RETURNS TRIGGER AS $$
BEGIN
  NEW.status_group := CASE
    WHEN NEW.status IN ('pending', 'waiting') THEN 'pending'
    WHEN NEW.status IN ('in_progress', 'building', 'snapshotting') THEN 'in_progress'
    WHEN NEW.status IN ('ready', 'uploaded', 'success') THEN 'ready'
    WHEN NEW.status = 'deleted' THEN 'deleted'
    ELSE 'failed'
  END;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION compute_status_group() RETURNS TRIGGER AS $$
BEGIN
  NEW.status_group := CASE
    WHEN NEW.status IN ('pending', 'waiting') THEN 'pending'
    WHEN NEW.status IN ('in_progress', 'building', 'snapshotting') THEN 'in_progress'
    WHEN NEW.status IN ('ready', 'uploaded', 'success') THEN 'ready'
    ELSE 'failed'
  END;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
