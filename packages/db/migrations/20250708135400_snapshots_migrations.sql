-- +goose Up
-- +goose StatementBegin
CREATE INDEX CONCURRENTLY idx_snapshots_sandbox_id ON public.snapshots (sandbox_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX CONCURRENTLY idx_snapshots_sandbox_id;
-- +goose StatementEnd
