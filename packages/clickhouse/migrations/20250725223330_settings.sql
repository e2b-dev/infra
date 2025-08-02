-- +goose Up
-- +goose StatementBegin
SET enable_json_type = 1, allow_experimental_json_type = 1, output_format_native_write_json_as_string = 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET enable_json_type = 0, allow_experimental_json_type = 0, output_format_native_write_json_as_string = 0;
-- +goose StatementEnd