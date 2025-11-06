-- +goose Up
-- +goose StatementBegin

ALTER TABLE snapshots
    ADD COLUMN firewall jsonb NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE snapshots
    DROP COLUMN IF EXISTS firewall;
-- +goose StatementEnd

