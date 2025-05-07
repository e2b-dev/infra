-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_env_builds_env_status_createdat_desc_idx ON env_builds (env_id, status, created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX CONCURRENTLY public.idx_env_builds_env_status_createdat_desc_idx;
-- +goose StatementEnd
