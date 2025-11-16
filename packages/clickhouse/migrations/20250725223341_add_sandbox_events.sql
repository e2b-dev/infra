-- +goose Up
-- +goose StatementBegin
CREATE TABLE sandbox_events as sandbox_events_local
    ENGINE = Distributed('cluster', currentDatabase(), 'sandbox_events_local', xxHash64(sandbox_id));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_events;
-- +goose StatementEnd

