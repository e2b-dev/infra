-- +goose Up
-- +goose StatementBegin
ALTER TABLE "public"."clusters" ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "public"."clusters" DISABLE ROW LEVEL SECURITY;
-- +goose StatementEnd
