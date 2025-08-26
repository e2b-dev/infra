-- +goose Up
-- +goose StatementBegin
CREATE TABLE product_usage as product_usage_local
    ENGINE = Distributed('cluster', currentDatabase(), 'product_usage_local', xxHash64(team_id));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS product_usage;
-- +goose StatementEnd

