-- +goose Up
-- +goose StatementBegin
UPDATE tiers SET "max_length_hours" = 720 WHERE id = 'base_v1' AND "max_length_hours" = 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE tiers SET "max_length_hours" = 1 WHERE id = 'base_v1' AND "max_length_hours" = 720;
-- +goose StatementEnd
