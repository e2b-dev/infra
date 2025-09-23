-- +goose Up
-- +goose StatementBegin
ALTER TABLE "public"."snapshots" ADD COLUMN "team_id" uuid NULL;
UPDATE "public"."snapshots" SET team_id = e.team_id FROM "public"."envs" e WHERE e.id = snapshots.env_id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE "public"."snapshots" DROP COLUMN "team_id";
-- +goose StatementEnd
