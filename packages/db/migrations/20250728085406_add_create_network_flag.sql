-- +goose Up
-- +goose StatementBegin
BEGIN;

ALTER TABLE snapshots
    ADD COLUMN allow_internet_access boolean NULL;

COMMIT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
BEGIN;

ALTER TABLE snapshots
    DROP COLUMN IF EXISTS allow_internet_access;

COMMIT;
-- +goose StatementEnd
