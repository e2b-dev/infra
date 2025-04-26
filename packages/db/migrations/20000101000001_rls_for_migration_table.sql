-- +goose Up
-- +goose StatementBegin
ALTER TABLE "public"."_migrations" ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- +goose StatementEnd
