-- +goose Up
-- +goose StatementBegin

CREATE SCHEMA IF NOT EXISTS auth_custom;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

/* We don't want to drop the schema, as it is used by other services. */

-- +goose StatementEnd
