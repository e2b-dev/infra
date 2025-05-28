-- +goose Up
-- +goose StatementBegin
ALTER TABLE public.env_builds
    ADD COLUMN ready_cmd      TEXT,
    ADD COLUMN ready_timeout  INTEGER;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE public.env_builds
DROP COLUMN ready_cmd,
    DROP COLUMN ready_timeout;
-- +goose StatementEnd