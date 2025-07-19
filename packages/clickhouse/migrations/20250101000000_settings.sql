-- +goose Up
-- +goose StatementBegin
SET enable_json_type = 1
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET enable_json_type = 0
-- +goose StatementEnd