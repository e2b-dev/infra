-- +goose Up
-- +goose StatementBegin

CREATE SCHEMA IF NOT EXISTS auth_custom;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP SCHEMA IF EXISTS auth_custom CASCADE;

-- +goose StatementEnd
