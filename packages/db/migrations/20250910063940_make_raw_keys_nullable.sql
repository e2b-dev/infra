-- +goose Up
-- +goose StatementBegin
ALTER TABLE "public"."access_tokens" ALTER COLUMN "access_token" DROP NOT NULL;
ALTER TABLE "public"."team_api_keys" ALTER COLUMN "api_key" DROP NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "public"."access_tokens" ALTER COLUMN "access_token" SET NOT NULL;
ALTER TABLE "public"."team_api_keys" ALTER COLUMN "api_key" SET NOT NULL;
-- +goose StatementEnd
