-- +goose Up
-- +goose StatementBegin
ALTER TABLE "public"."snapshots" ALTER COLUMN "team_id" SET NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "public"."snapshots" ALTER COLUMN "team_id" DROP NOT NULL;
-- +goose StatementEnd
