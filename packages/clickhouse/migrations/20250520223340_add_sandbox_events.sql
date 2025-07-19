-- +goose Up
-- +goose StatementBegin
CREATE TABLE sandbox_events (
    timestamp DateTime64(9),
    sandbox_id UUID,
    sandbox_execution_id UUID,
    sandbox_template_id UUID,
    sandbox_team_id UUID,
    event_category String,
    event_label String,
    event_data Nullable(JSON)
) ENGINE = MergeTree ORDER BY (timestamp, sandbox_id, event_category, event_label);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS sandbox_events;
-- +goose StatementEnd
