-- +goose Up
-- +goose StatementBegin
ALTER TABLE volumes ADD COLUMN quota BIGINT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE volumes DROP COLUMN quota;
-- +goose StatementEnd
