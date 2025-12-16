-- +goose Up

-- Drop product usage global
DROP TABLE IF EXISTS product_usage;

-- Drop product usage local
DROP TABLE IF EXISTS product_usage_local;

-- +goose Down
-- Create product usage local
CREATE TABLE product_usage_local (
    timestamp DateTime64(9) CODEC (Delta, ZSTD(1)),
    team_id UUID CODEC (ZSTD(1)),
    category LowCardinality(String) CODEC (ZSTD(1)),
    action LowCardinality(String) CODEC (ZSTD(1)),
    label String CODEC (ZSTD(1))
) ENGINE = MergeTree
    PARTITION BY toDate(timestamp)
    ORDER BY (timestamp, team_id, category, action)

-- Create product usage global
CREATE TABLE product_usage as product_usage_local
    ENGINE = Distributed('cluster', currentDatabase(), 'product_usage_local', xxHash64(team_id));
