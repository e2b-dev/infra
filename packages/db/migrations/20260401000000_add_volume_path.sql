-- +goose Up
ALTER TABLE volumes ADD COLUMN volume_path TEXT;

-- +goose Down
ALTER TABLE volumes DROP COLUMN IF EXISTS volume_path;
