-- +goose Up
-- +goose StatementBegin
CREATE TABLE metrics_gauge as metrics_gauge_local
    ENGINE = Distributed('cluster', currentDatabase(), 'metrics_gauge_local', xxHash64(Attributes));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS metrics_gauge;
-- +goose StatementEnd

