-- +goose Up
-- +goose StatementBegin
CREATE TABLE product_usage_local (
    timestamp DateTime64(9) CODEC (Delta, ZSTD(1)),
    team_id UUID CODEC (ZSTD(1)),
    category LowCardinality(String) CODEC (ZSTD(1)),
    action LowCardinality(String) CODEC (ZSTD(1)),
    label String CODEC (ZSTD(1))
) ENGINE = MergeTree 
    PARTITION BY toDate(timestamp)
    ORDER BY (timestamp, team_id, category, action)
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS product_usage_local;
-- +goose StatementEnd
